package session

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// Direct usage (§9.6): the provider counts the bytes of the direct pipes of
// each link and reports them cumulatively; the server keeps the last value
// of every link and counts the increase. Statistics only.

const (
	// DefaultUsageInterval is how often a provider reports.
	DefaultUsageInterval = time.Minute
	// usageKeep is how long the server accepts reports of an ended link.
	usageKeep = 5 * time.Minute
	// usageSample is the longest time between two looks at the pipes of a
	// link. A pipe that is removed counts up to its last look, so this bounds
	// what a removal can lose.
	usageSample = time.Second
	// usageWriteTimeout bounds opening the stream and writing one report.
	usageWriteTimeout = 10 * time.Second
)

// usageIntervalOf is DialConfig.UsageInterval as a period; 0 means the
// default and negative off (returned as 0).
func usageIntervalOf(d time.Duration) time.Duration {
	switch {
	case d == 0:
		return DefaultUsageInterval
	case d < 0:
		return 0
	}
	return d
}

// directMeter adds up the bytes of the direct pipes of one nested session.
// A pipe that left the session is folded into the total with what it was
// last seen to carry, so the total never decreases.
type directMeter struct {
	mu      sync.Mutex
	in, out uint64 // of the pipes gone
	live    map[uint64]pipeBytes
}

type pipeBytes struct{ in, out uint64 }

// sample updates the meter from a snapshot of the session's pipes.
func (m *directMeter) sample(stats []zmux.PipeStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[uint64]bool, len(stats))
	if m.live == nil {
		m.live = map[uint64]pipeBytes{}
	}
	for _, p := range stats {
		if !isDirectPipe(p) {
			continue
		}
		seen[p.Pipe] = true
		old := m.live[p.Pipe]
		m.live[p.Pipe] = pipeBytes{max(old.in, p.BytesReceived), max(old.out, p.BytesSent)}
	}
	for id, b := range m.live {
		if !seen[id] {
			m.in, m.out = m.in+b.in, m.out+b.out
			delete(m.live, id)
		}
	}
}

// total is the cumulative bytes received and sent over the direct pipes.
func (m *directMeter) total() (in, out uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, out = m.in, m.out
	for _, b := range m.live {
		in, out = in+b.in, out+b.out
	}
	return in, out
}

// usageReport is what a link last reported to the server.
type usageReport struct{ in, out uint64 }

// sampleUsage looks at the pipes of the link's nested session.
func (p *providerLink) sampleUsage(zc *zmux.Conn) { p.meter.sample(zc.PipeStats()) }

// flushUsage reports the link's cumulative direct bytes if they changed
// since the last report. It is best effort: a failure leaves the last value
// as it was, and the next report carries everything. It tells whether a
// report was sent.
func (p *providerLink) flushUsage() bool {
	in, out := p.meter.total()
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	if in == p.reported.in && out == p.reported.out {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), usageWriteTimeout)
	defer cancel()
	st, err := p.s.zc.OpenStreamSync(ctx)
	if err != nil {
		return false
	}
	lc := &linkConn{Stream: st}
	if err := st.SetPreamble(wire.AppendUsageOpen()); err != nil {
		lc.abort(wire.CodeInternal)
		return false
	}
	_ = lc.SetWriteDeadline(time.Now().Add(usageWriteTimeout))
	if _, err := lc.Write(wire.AppendUsage(nil, []wire.LinkUsage{{LinkID: p.id, In: in, Out: out}})); err != nil {
		lc.abort(wire.CodeInternal)
		return false
	}
	_ = lc.Close()
	p.reported = usageReport{in, out}
	return true
}

// runUsage samples the link's pipes and reports on the interval until the
// link ends.
func (p *providerLink) runUsage(zc *zmux.Conn) {
	every := usageIntervalOf(p.s.d.cfg.UsageInterval)
	if every == 0 {
		return
	}
	tick := time.NewTicker(min(every, usageSample))
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-tick.C:
			p.sampleUsage(zc)
			if time.Since(last) >= every {
				last = time.Now()
				p.flushUsage()
			}
		}
	}
}

// finalUsage is the last report of a link, before its LINK closes.
func (p *providerLink) finalUsage(zc *zmux.Conn) {
	if usageIntervalOf(p.s.d.cfg.UsageInterval) == 0 {
		return
	}
	p.sampleUsage(zc)
	p.flushUsage()
}

// linkUsage is the server's record of a link's reports.
type linkUsage struct {
	provider int
	add      func(in, out uint64)
	in, out  uint64
	expires  time.Time // zero while the link lives
}

// trackUsage starts the record of a provider link.
func (a *Acceptor) trackUsage(l *peerLink) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	a.usage[l.id] = &linkUsage{provider: l.grant.Provider, add: l.grant.AddDirect}
}

// endUsage lets the record of an ended link stand for usageKeep more, and
// drops the ones past it.
func (a *Acceptor) endUsage(id uint64) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	now := a.clock()
	if u := a.usage[id]; u != nil && u.expires.IsZero() {
		u.expires = now.Add(usageKeep)
	}
	for k, u := range a.usage {
		if !u.expires.IsZero() && now.After(u.expires) {
			delete(a.usage, k)
		}
	}
}

// addUsage counts one entry reported by provider: the increase over the last
// value. An entry of a link that is not the provider's, unknown or past its
// keep time is ignored.
func (a *Acceptor) addUsage(provider int, e wire.LinkUsage) {
	a.usageMu.Lock()
	now := a.clock()
	u := a.usage[e.LinkID]
	if u == nil || u.provider != provider || !u.expires.IsZero() && now.After(u.expires) {
		a.usageMu.Unlock()
		return
	}
	din, dout := e.In-min(e.In, u.in), e.Out-min(e.Out, u.out)
	u.in, u.out = max(u.in, e.In), max(u.out, e.Out)
	add := u.add
	a.usageMu.Unlock()
	if add != nil && (din != 0 || dout != 0) {
		add(din, dout)
	}
}

// serveUsage reads the body of a P2P_USAGE stream until its end (§9.6).
func (s *serverSession) serveUsage(lc *linkConn) {
	defer func() { _ = lc.Close() }()
	if !s.t.hello.HasCap(wire.CapLink) {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	_ = lc.SetReadDeadline(time.Now().Add(usageWriteTimeout))
	body, err := io.ReadAll(io.LimitReader(lc, wire.MaxUsageBody+1))
	if err != nil {
		return
	}
	if len(body) > wire.MaxUsageBody {
		lc.abort(wire.CodeMalformed)
		return
	}
	list, err := wire.ParseUsage(body)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	for _, e := range list {
		s.a.addUsage(s.ClientID(), e)
	}
}
