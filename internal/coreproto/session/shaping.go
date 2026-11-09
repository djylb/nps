package session

import (
	"bytes"
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/shaping"
	"github.com/zmuxio/zmux-go/shaping/bundle"
	"github.com/zmuxio/zmux-go/shaping/machine"
)

// shapingPlan is a parsed bundle, compiled per carrier kind. It is immutable.
type shapingPlan struct {
	raw        []byte
	b          *bundle.Bundle
	compiled   map[*bundle.Entry]*machine.Compiled
	handshakes map[string]shaping.HandshakeShaper // by HandshakeView.Kind
}

func newShapingPlan(raw []byte) (*shapingPlan, error) {
	b, err := bundle.Parse(raw, bundle.ParseOptions{})
	if err != nil {
		return nil, err
	}
	p := &shapingPlan{raw: bytes.Clone(raw), b: b, compiled: map[*bundle.Entry]*machine.Compiled{},
		handshakes: map[string]shaping.HandshakeShaper{}}
	// Every kind compiled now: the factory runs under the session lock.
	for k := range 256 {
		kind := zmux.CarrierKind(k)
		e := b.Entry(kind)
		c, ok := p.compiled[e]
		if !ok {
			c = e.Candidate.Compile(machine.Options{})
			p.compiled[e] = c
		}
		if name := bundle.KindName(kind); name != bundle.DefaultKind || k == 0 {
			if k == 0 {
				name = ""
			}
			p.handshakes[name] = c.Handshake()
		}
	}
	return p, nil
}

// handshake is the plan of a handshake message on the view's carrier kind.
func (p *shapingPlan) handshake(v *shaping.HandshakeView, u *shaping.UnitPlan) bool {
	h, ok := p.handshakes[v.Kind]
	if !ok {
		h = p.handshakes[""]
	}
	return h(v, u)
}

// sessionShaper is what SetShaper takes for a session started on boot: the
// records of each pipe follow its own kind, the budget that of boot. A nil p
// is no shaping.
func (p *shapingPlan) sessionShaper(boot zmux.CarrierKind) (zmux.ShaperFactory, func() shaping.Budget) {
	if p == nil {
		return nil, nil
	}
	return func(sp zmux.ShapedPipe) shaping.Shaper {
		f := p.compiled[p.b.Entry(sp.Carrier)].Factory()
		if f == nil {
			return nil
		}
		return f(sp)
	}, p.compiled[p.b.Entry(boot)].Budget()
}

// nestedShaper is what a nested session takes: the entry of its direct paths,
// and nothing on a relay, whose stream the outer session shapes. A nil p is no
// shaping.
func (p *shapingPlan) nestedShaper() (zmux.ShaperFactory, func() shaping.Budget) {
	f, budget := p.sessionShaper(zmux.CarrierQUIC)
	if f == nil {
		return nil, nil
	}
	return func(sp zmux.ShapedPipe) shaping.Shaper {
		if !isDirectCarrier(sp.Carrier) {
			return nil
		}
		return f(sp)
	}, budget
}

// SetShaping replaces the bundle used with the clients whose switch is on, nil
// for none, and brings their sessions in line. A bundle that does not parse is
// ignored.
func (a *Acceptor) SetShaping(b []byte) {
	var p *shapingPlan
	if len(b) > 0 {
		var err error
		if p, err = newShapingPlan(b); err != nil {
			log.Warn("shaping bundle not applied", "err", err)
			return
		}
	}
	a.shaping.Store(p)
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

// shapingOn is the bundle of a client with switch on, before its capabilities
// are known.
func (a *Acceptor) shapingOn(on bool) *shapingPlan {
	if !on {
		return nil
	}
	return a.shaping.Load()
}

// shapingFor is the bundle of an admitted client: its switch is on and it
// advertised the capability (§13).
func (a *Acceptor) shapingFor(t *ticket, on bool) *shapingPlan {
	if !t.hello.HasCap(wire.CapShaping) {
		return nil
	}
	return a.shapingOn(on)
}

// follow makes p the session's shaping, if it is not already.
func (s *serverSession) follow(p *shapingPlan) {
	if s.t.plan.Swap(p) == p {
		return
	}
	f, budget := p.sessionShaper(s.t.kind)
	s.zc.SetShaper(f, budget)
}

// sessionConfig is zmux.ListenerConfig.SessionConfig: base, shaped from the
// first record when the client is to be.
func (a *Acceptor) sessionConfig(base *zmux.Config, admitted any) *zmux.Config {
	peer, ok := admitted.(*seal.Peer)
	if !ok {
		return nil
	}
	t, ok := peer.Value.(*ticket)
	if !ok {
		return nil
	}
	var p *shapingPlan
	if e := a.table.get(t.client); e != nil {
		p = a.shapingFor(t, e.Shaping)
	}
	t.plan.Store(p)
	cfg := base.Clone()
	cfg.HandshakeShaper = t.joinShape
	cfg.Shaper, cfg.ShapingBudget = p.sessionShaper(t.kind)
	return cfg
}

// joinShape plans the Join messages of the session; true without a bundle, so
// that no message counts as unplanned.
func (t *ticket) joinShape(v *shaping.HandshakeView, u *shaping.UnitPlan) bool {
	if p := t.plan.Load(); p != nil {
		return p.handshake(v, u)
	}
	return true
}

// handshake plans the reply of the Establish whose Handle Resolve saw; a
// client's capabilities come in the message after it.
func (g *gate) handshake(v *shaping.HandshakeView, u *shaping.UnitPlan) bool {
	if g.entry == nil {
		return false
	}
	p := g.a.shapingOn(g.entry.Shaping)
	return p != nil && p.handshake(v, u)
}

// planSlot holds, for this process only, the latest bundle of a server and
// key, and the nested sessions that follow it: a first connection has none,
// the later ones start with it.
type planSlot struct {
	plan atomic.Pointer[shapingPlan]

	mu     sync.Mutex // serializes changes of plan and nested
	nested map[*nestedShaping]struct{}
}

var planSlots sync.Map // string -> *planSlot

func planSlotOf(key string) *planSlot {
	v, _ := planSlots.LoadOrStore(key, &planSlot{nested: map[*nestedShaping]struct{}{}})
	return v.(*planSlot)
}

func (d *Dialer) plan() *shapingPlan { return d.plans.plan.Load() }

func (d *Dialer) keepPlan(p *shapingPlan) { d.plans.keep(p) }

// keep makes p the bundle, and that of every nested session.
func (s *planSlot) keep(p *shapingPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan.Store(p)
	for n := range s.nested {
		n.follow(p)
	}
}

// handshake plans the handshake messages of nested sessions: those on a direct
// path, by the bundle at hand. true otherwise, so that none counts as
// unplanned.
func (s *planSlot) handshake(v *shaping.HandshakeView, u *shaping.UnitPlan) bool {
	if v.Kind != zmux.CarrierQUIC.String() {
		return true
	}
	if p := s.plan.Load(); p != nil {
		return p.handshake(v, u)
	}
	return true
}

// nestedShaping ties one nested session to the bundle of its end of the link.
type nestedShaping struct {
	slot *planSlot
	used atomic.Pointer[shapingPlan] // the bundle the session has
	zc   *zmux.Conn                  // set by attach; under slot.mu
}

func (d *Dialer) nestedShaping() *nestedShaping { return &nestedShaping{slot: d.plans} }

// configure shapes the session cfg starts from its first record.
func (n *nestedShaping) configure(cfg *zmux.Config) *zmux.Config {
	p := n.slot.plan.Load()
	n.used.Store(p)
	cfg.HandshakeShaper = n.slot.handshake
	cfg.Shaper, cfg.ShapingBudget = p.nestedShaper()
	return cfg
}

// attach makes the started session follow the bundle, a change since configure
// included.
func (n *nestedShaping) attach(zc *zmux.Conn) {
	n.slot.mu.Lock()
	defer n.slot.mu.Unlock()
	n.zc = zc
	n.slot.nested[n] = struct{}{}
	n.follow(n.slot.plan.Load())
}

func (n *nestedShaping) detach() {
	n.slot.mu.Lock()
	defer n.slot.mu.Unlock()
	delete(n.slot.nested, n)
}

// follow makes p the session's bundle, with slot.mu held and the session
// attached.
func (n *nestedShaping) follow(p *shapingPlan) {
	if n.used.Swap(p) == p {
		return
	}
	f, budget := p.nestedShaper()
	n.zc.SetShaper(f, budget)
}

// joinShape plans the Join messages of the Dialer's sessions; true without a
// bundle, so that no message counts as unplanned.
func (d *Dialer) joinShape(v *shaping.HandshakeView, u *shaping.UnitPlan) bool {
	if p := d.plan(); p != nil {
		return p.handshake(v, u)
	}
	return true
}

// establishShape is the Shaper of the next Establish, nil without a bundle.
func establishShape(p *shapingPlan) shaping.HandshakeShaper {
	if p == nil {
		return nil
	}
	return p.handshake
}

// applyShaping follows the snapshot's shaping section; a bundle that does not
// parse is ignored, the session going on as it was.
func (s *clientSession) applyShaping(raw []byte) {
	var next *shapingPlan
	if len(raw) > 0 {
		if cur := s.d.plan(); cur != nil && bytes.Equal(raw, cur.raw) {
			next = cur
		} else {
			var err error
			if next, err = newShapingPlan(raw); err != nil {
				log.Warn("shaping bundle not applied", "err", err)
				return
			}
		}
	}
	s.d.keepPlan(next)
	if next == s.plan {
		return
	}
	s.plan = next
	f, budget := next.sessionShaper(s.boot)
	s.zc.SetShaper(f, budget)
}
