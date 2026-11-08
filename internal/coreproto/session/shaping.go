package session

import (
	"bytes"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/shaping"
	"github.com/zmuxio/zmux-go/shaping/bundle"
	"github.com/zmuxio/zmux-go/shaping/machine"
)

// SetShaping replaces the bundle given to the clients whose switch is on, nil
// for none, and pushes the change to them.
func (a *Acceptor) SetShaping(b []byte) {
	if b == nil {
		a.shaping.Store(nil)
	} else {
		a.shaping.Store(&b)
	}
	a.mu.Lock()
	var all []*serverSession
	for _, l := range a.sess {
		all = append(all, l...)
	}
	a.mu.Unlock()
	for _, s := range all {
		s.kick()
	}
}

// shapingFor is the shaping section of s: the bundle, when the client's
// switch is on and it advertised the capability (§13).
func (a *Acceptor) shapingFor(s *serverSession, on bool) []byte {
	b := a.shaping.Load()
	if b == nil || !on || !s.t.hello.HasCap(wire.CapShaping) {
		return nil
	}
	return *b
}

// applyShaping follows the snapshot's shaping section; a bundle that does not
// parse is ignored, the session going on as it was.
func (s *clientSession) applyShaping(raw []byte) {
	if bytes.Equal(raw, s.shapingRaw) {
		return
	}
	if len(raw) == 0 {
		s.shapingRaw = nil
		s.zc.SetShaper(nil, nil)
		return
	}
	b, err := bundle.Parse(raw, bundle.ParseOptions{})
	if err != nil {
		log.Warn("shaping bundle not applied", "err", err)
		return
	}
	s.shapingRaw = bytes.Clone(raw)
	f, budget := shaperOf(b)
	s.zc.SetShaper(f, budget)
}

// shaperOf compiles what b holds per carrier kind; the session's budget is
// the one of a pipe of no listed kind.
func shaperOf(b *bundle.Bundle) (zmux.ShaperFactory, func() shaping.Budget) {
	compile := func(e *bundle.Entry) *machine.Compiled {
		if e == nil {
			return nil
		}
		return e.Candidate.Compile(machine.Options{})
	}
	compiled := map[*bundle.Entry]*machine.Compiled{}
	entry := func(k zmux.CarrierKind) *machine.Compiled {
		e := b.Entry(k)
		c, ok := compiled[e]
		if !ok {
			c = compile(e)
			compiled[e] = c
		}
		return c
	}
	// Every kind compiled now: the factory runs under the session lock.
	for k := range 256 {
		entry(zmux.CarrierKind(k))
	}
	var budget func() shaping.Budget
	if c := entry(0); c != nil {
		budget = c.Budget()
	}
	return func(p zmux.ShapedPipe) shaping.Shaper {
		c := compiled[b.Entry(p.Carrier)]
		if c == nil {
			return nil
		}
		f := c.Factory()
		if f == nil {
			return nil
		}
		return f(p)
	}, budget
}
