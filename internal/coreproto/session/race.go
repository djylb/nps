package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/connprofile"
)

// raceStagger is how long an attempt of a type=auto race has before the next
// one starts beside it.
const raceStagger = 250 * time.Millisecond

// autoDropWindow is how long a session must last for its entry to count as
// good: a path that lets the handshake through and then breaks is not.
const autoDropWindow = 30 * time.Second

var errRaceLost = errors.New("another carrier won")

// raceGate lets one attempt of a race send its Hello at a time. Another that
// gets there meanwhile waits for the outcome: the holder's failure is its
// turn, the holder's success the end of it, before it has told the server
// anything.
type raceGate struct {
	tok  chan struct{}
	won  chan struct{}
	once sync.Once
}

func newRaceGate() *raceGate {
	return &raceGate{tok: make(chan struct{}, 1), won: make(chan struct{})}
}

func (g *raceGate) acquire(ctx context.Context) error {
	select {
	case g.tok <- struct{}{}:
		return nil
	case <-g.won:
		return errRaceLost
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *raceGate) release() { <-g.tok }

func (g *raceGate) win() { g.once.Do(func() { close(g.won) }) }

type raceGateKey struct{}

func raceGateOf(ctx context.Context) *raceGate {
	g, _ := ctx.Value(raceGateKey{}).(*raceGate)
	return g
}

// autoKey is the cache key of an entry (candidate plus network).
func autoKey(raw, network string) string { return raw + "|" + network }

// autoRank orders entries: those that connected on this network, others, then
// those whose session broke soon after connecting.
func (d *Dialer) autoRank(raw, network string) int {
	switch k := autoKey(raw, network); {
	case d.autoDropped[k]:
		return 2
	case d.autoOK[k]:
		return 0
	}
	return 1
}

// raceGroups returns the entries of the attempts in the order to dial them,
// the attempts of one entry together, standby entries in a round of their own.
func (d *Dialer) raceGroups(cands []connprofile.Candidate, network string) (first, standby [][]connprofile.Candidate) {
	index := map[string]int{}
	var groups [][]connprofile.Candidate
	for _, c := range cands {
		i, ok := index[c.Raw]
		if !ok {
			i = len(groups)
			index[c.Raw] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], c)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	slices.SortStableFunc(groups, func(a, b []connprofile.Candidate) int {
		if r := d.autoRank(a[0].Raw, network) - d.autoRank(b[0].Raw, network); r != 0 {
			return r
		}
		return a[0].Tier - b[0].Tier
	})
	for _, g := range groups {
		if g[0].Standby {
			standby = append(standby, g)
		} else {
			first = append(first, g)
		}
	}
	return first, standby
}

// dialRace dials type=auto: the entries staggered, one winner, standby ones
// only when all others failed. A round that fails is one failure of Dial.
func (d *Dialer) dialRace(ctx context.Context, cands []connprofile.Candidate) (*clientSession, error) {
	network := currentNetwork()
	first, standby := d.raceGroups(cands, network)
	var errs []error
	for _, set := range [][][]connprofile.Candidate{first, standby} {
		if len(set) == 0 {
			continue
		}
		s, c, err := d.raceSet(ctx, set)
		if err == nil {
			d.mu.Lock()
			d.last, d.autoWinner = c.Key(), c.Raw
			d.autoOK[autoKey(c.Raw, network)] = true
			d.mu.Unlock()
			s.mu.Lock()
			s.autoEntry, s.autoStart = autoKey(c.Raw, network), d.now()
			s.mu.Unlock()
			return s, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil, errors.New("no candidate can start a session")
	}
	return nil, errors.Join(errs...)
}

// raceResult is the outcome of one entry of a race.
type raceResult struct {
	s   *clientSession
	c   connprofile.Candidate
	err error
}

// raceSet dials set, one entry and, every raceStagger or at a failure, the
// next. It waits for every attempt it started; the session it returns is the
// only one the server saw committed.
func (d *Dialer) raceSet(ctx context.Context, set [][]connprofile.Candidate) (*clientSession, connprofile.Candidate, error) {
	gate := newRaceGate()
	rctx, cancel := context.WithCancel(context.WithValue(ctx, raceGateKey{}, gate))
	defer cancel()
	results := make(chan raceResult, len(set))
	var wg sync.WaitGroup
	defer wg.Wait()
	start := func(g []connprofile.Candidate) {
		wg.Go(func() { results <- d.dialEntry(rctx, g, gate) })
	}
	start(set[0])
	next, pending := 1, 1
	t := time.NewTimer(raceStagger)
	defer t.Stop()
	var errs []error
	for pending > 0 {
		select {
		case r := <-results:
			pending--
			if r.err == nil {
				cancel()
				return r.s, r.c, nil
			}
			if !errors.Is(r.err, errRaceLost) {
				errs = append(errs, fmt.Errorf("%s: %w", r.c.Raw, r.err))
			}
			if next < len(set) {
				start(set[next])
				next++
				pending++
				t.Reset(raceStagger)
			}
		case <-t.C:
			if next < len(set) {
				start(set[next])
				next++
				pending++
				t.Reset(raceStagger)
			}
		}
	}
	return nil, connprofile.Candidate{}, errors.Join(errs...)
}

// dialEntry tries the attempts of one entry in turn, the first that
// establishes wins the race.
func (d *Dialer) dialEntry(ctx context.Context, attempts []connprofile.Candidate, gate *raceGate) raceResult {
	var last error
	var c connprofile.Candidate
	for _, c = range attempts {
		if !c.Starts() {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, d.attemptTimeout(c))
		s, err := d.attempt(dctx, c)
		cancel()
		d.noteH3(c, err == nil, err)
		if err == nil {
			gate.win()
			return raceResult{s: s, c: c}
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return raceResult{c: c, err: last}
}

// autoEnded applies the drop window to the session's entry.
func (d *Dialer) autoEnded(s *clientSession) {
	s.mu.Lock()
	entry, start := s.autoEntry, s.autoStart
	s.mu.Unlock()
	if entry == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.now().Sub(start) < autoDropWindow {
		delete(d.autoOK, entry)
		d.autoDropped[entry] = true
		return
	}
	delete(d.autoDropped, entry)
}

// currentNetwork names the addresses the host has now, what the cache of a
// race is per network.
func currentNetwork() string {
	ifaces, _ := net.Interfaces()
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			out = append(out, ifc.Name+"="+a.String())
		}
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}
