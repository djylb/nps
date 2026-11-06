package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/healthcheck"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/upstream"
	"github.com/djylb/upstream/health"
	"github.com/djylb/upstream/uphttp"
)

// healthSink is how reports reach the server. Its call returns within a bounded
// time; after an error it is not called again.
type healthSink interface {
	ReportHealth(healthcheck.Report) error
}

// healthKey identifies one check: groups asking for the same check share it.
type healthKey struct {
	spec healthSpec
	addr string
}

type healthSpec struct {
	typ, path               string
	interval, timeout, fall int
}

type healthVerdict struct {
	generation uint64
	up         bool
}

// HealthChecker runs the checks of the groups the server gave, and reports to the server when a target goes down or comes up.
//
// Nothing here waits on the network: callbacks and the signal reader queue
// reports, and one goroutine writes them. It is the only writer of reports in
// npc, so messages never interleave.
type HealthChecker struct {
	checker  *health.Checker[healthKey, string]
	reporter *healthReporter

	mu sync.RWMutex // never held across a wait; taken before the checker's own
	// fails counts the failing checks of each target that is down.
	fails map[healthKey]int
	// verdicts holds the latest verdict per key, for a group that joins a
	// running check: a report goes out only when the health changes.
	verdicts map[healthKey]healthVerdict
	// byAddr finds the group checks of an address for real traffic.
	byAddr map[string]map[healthKey]struct{}
}

func NewHealthChecker(parentCtx context.Context, sink healthSink) *HealthChecker {
	hc := &HealthChecker{
		reporter: newHealthReporter(sink),
		fails:    make(map[healthKey]int),
		verdicts: make(map[healthKey]healthVerdict),
		byAddr:   make(map[string]map[healthKey]struct{}),
	}
	hc.checker = health.New[healthKey, string](parentCtx, health.Config[healthKey]{
		OnResult: hc.onResult,
		OnRemove: hc.onRemove,
	})
	return hc
}

// healthProbe returns the check of a type.
func healthProbe(typ, path string) health.Probe {
	switch typ {
	case "tcp":
		return health.TCP(health.TCPOptions{})
	case "http", "https":
		return uphttp.Probe(uphttp.Check{
			Scheme: typ,
			Path:   path,
			Status: uphttp.StatusRange{Min: 200, Max: 200},
		})
	default:
		err := fmt.Errorf("unsupported health check type: %s", typ)
		return func(context.Context, health.Target) (health.Observation, error) {
			return health.Observation{}, err
		}
	}
}

// setGroup adds the targets of m to its group. Identical checks of other groups
// are shared; a group that joins one learns its verdict at once.
func (hc *HealthChecker) setGroup(m healthcheck.Group) {
	if m.Name == "" || m.Interval <= 0 || m.Timeout <= 0 || m.Fall <= 0 {
		logs.Warn("Ignoring invalid health group %q", m.Name)
		return
	}
	spec := healthSpec{typ: m.Type, path: m.Path, interval: m.Interval, timeout: m.Timeout, fall: m.Fall}
	t := health.Target{Probe: healthProbe(m.Type, m.Path), Options: health.Options{
		Interval: time.Duration(m.Interval) * time.Second,
		Timeout:  time.Duration(m.Timeout) * time.Second,
		Rise:     1,
		Fall:     m.Fall,
	}}
	for _, addr := range m.Targets {
		key := healthKey{spec: spec, addr: addr}
		t.Address = common.FormatAddress(addr)
		generation := hc.checker.AddToGroup(m.Name, key, t)
		hc.mu.Lock()
		keys := hc.byAddr[t.Address]
		if keys == nil {
			keys = make(map[healthKey]struct{})
			hc.byAddr[t.Address] = keys
		}
		keys[key] = struct{}{}
		if v, ok := hc.verdicts[key]; ok && v.generation == generation {
			hc.reporter.report(m.Name, addr, healthStatus(v.up))
		}
		hc.mu.Unlock()
	}
}

// delGroup removes a group; checks nothing else holds stop and are released.
func (hc *HealthChecker) delGroup(group string) {
	keys := hc.checker.GroupKeys(group)
	hc.checker.RemoveGroup(group)
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for _, key := range keys {
		if _, held := hc.checker.Status(key); held {
			continue
		}
		addr := common.FormatAddress(key.addr)
		delete(hc.byAddr[addr], key)
		if len(hc.byAddr[addr]) == 0 {
			delete(hc.byAddr, addr)
		}
	}
}

// touch notes that real traffic just reached addr, so that it is not probed
// while in use.
func (hc *HealthChecker) touch(addr string) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	for key := range hc.byAddr[addr] {
		hc.checker.Touch(key)
	}
}

// dialFailed checks addr now rather than at its time.
func (hc *HealthChecker) dialFailed(addr string) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	for key := range hc.byAddr[addr] {
		hc.checker.CheckNow(key)
	}
}

func healthStatus(up bool) string {
	if up {
		return "1"
	}
	return "0"
}

// onResult reports a target down when it fails Fall times in a row, and again
// after every further Fall failures, which also repairs a verdict the server
// lost, and reports it up on its first verdict or first pass after that: the
// server knows nothing yet.
func (hc *HealthChecker) onResult(r health.Report[healthKey]) {
	if r.Health == upstream.HealthUnknown {
		return
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	up, send := r.Health == upstream.HealthUp, false
	if up {
		delete(hc.fails, r.Key)
		send = r.Previous != upstream.HealthUp
	} else {
		if r.Changed() {
			hc.fails[r.Key] = 0
		}
		hc.fails[r.Key]++
		send = (hc.fails[r.Key]-1)%r.Target.Options.Fall == 0
	}
	hc.verdicts[r.Key] = healthVerdict{generation: r.Generation, up: up}
	if !send {
		return
	}
	for _, group := range hc.checker.GroupsOf(r.Key) {
		hc.reporter.report(group, r.Key.addr, healthStatus(up))
	}
}

func (hc *HealthChecker) onRemove(key healthKey, generation uint64) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if v, ok := hc.verdicts[key]; ok && v.generation == generation {
		delete(hc.verdicts, key)
		delete(hc.fails, key)
	}
}

// Stop ends all checks and reports, waiting for a report being written.
func (hc *HealthChecker) Stop() {
	hc.checker.Close()
	<-hc.checker.Done()
	hc.reporter.stop()
}

// healthReporter writes reports on one goroutine. Reports wait in a queue
// holding the latest status per group and address, so it is bounded by the
// number of checks however slow the connection is.
type healthReporter struct {
	sink    healthSink
	mu      sync.Mutex
	pending map[healthReportKey]string
	order   []healthReportKey
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
	once    sync.Once
}

type healthReportKey struct{ group, addr string }

func newHealthReporter(sink healthSink) *healthReporter {
	r := &healthReporter{
		sink:    sink,
		pending: make(map[healthReportKey]string),
		wake:    make(chan struct{}, 1),
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *healthReporter) report(group, addr, status string) {
	key := healthReportKey{group, addr}
	r.mu.Lock()
	if _, queued := r.pending[key]; !queued {
		r.order = append(r.order, key)
	}
	r.pending[key] = status
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *healthReporter) next() (healthReportKey, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) == 0 {
		return healthReportKey{}, "", false
	}
	key := r.order[0]
	r.order = r.order[1:]
	status := r.pending[key]
	delete(r.pending, key)
	return key, status, true
}

func (r *healthReporter) run() {
	defer close(r.done)
	for {
		select {
		case <-r.quit:
			return
		case <-r.wake:
		}
		for {
			key, status, ok := r.next()
			if !ok {
				break
			}
			if err := r.sink.ReportHealth(healthcheck.Report{Group: key.group, Addr: key.addr, Up: status == "1"}); err != nil {
				logs.Trace("send health info of %s: %v", key.addr, err)
				return
			}
			select {
			case <-r.quit:
				return
			default:
			}
		}
	}
}

func (r *healthReporter) stop() {
	r.once.Do(func() { close(r.quit) })
	<-r.done
}
