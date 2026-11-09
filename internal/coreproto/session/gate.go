package session

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
)

var (
	errUnknownHandle = errors.New("unknown handle")
	errRefused       = errors.New("refused")
)

// ticket is the Gate's verdict, read back as Peer.Value.
type ticket struct {
	client  int
	handle  [wire.HandleLen]byte // of the vkey it was admitted under
	crypt   bool
	hello   wire.Hello
	static  []byte
	src     netip.AddrPort
	carrier string                       // set after the handshake (admitPipe)
	kind    zmux.CarrierKind             // likewise
	plan    *atomic.Pointer[shapingPlan] // the session's shaping
}

// gate is the seal.Gate of the entry.
type gate struct {
	a     *Acceptor
	entry *clientEntry // of the Handle Resolve saw
}

func (g *gate) Resolve(_ context.Context, handle []byte, _ netip.AddrPort) ([]byte, error) {
	if e := g.a.table.lookup(handle); e != nil {
		g.entry = e
		return e.psk, nil
	}
	return nil, errUnknownHandle
}

// Credential sends none: a client's Anchor trusts the psk0 proof (§3.3).
func (g *gate) Credential(context.Context, seal.CredentialRequest) (*seal.Credential, error) {
	return nil, nil
}

// Commit is where a handshake that passed msg3 becomes a client's. It decides
// Trusted per client, since AllowRecordMode runs before the client is known
// (§3.2).
func (g *gate) Commit(_ context.Context, a seal.Admission) (any, error) {
	e := g.a.table.lookup(a.Handle)
	if e == nil {
		return nil, errUnknownHandle
	}
	refuse := func(reason, flag string) (any, error) {
		g.a.authFailed(coreapi.AuthFailure{Source: a.Source, Client: e.ID, Reason: reason, Flag: flag})
		return nil, errRefused
	}
	if !e.Enabled {
		return refuse("disabled", "")
	}
	if a.Peer.Mode == zmux.Trusted && e.Crypt {
		return refuse("forbidden", "trusted")
	}
	var hello wire.Hello
	for _, d := range a.Peer.AppData {
		if d.Message != 3 {
			continue
		}
		var err error
		if hello, err = wire.ParseHello(d.Data); err != nil {
			return refuse("malformed", "")
		}
	}
	t := &ticket{client: e.ID, handle: e.handle, crypt: e.Crypt, hello: hello, static: a.Peer.Static, src: a.Source, plan: new(atomic.Pointer[shapingPlan])}
	if !g.a.admitFirstPipe(t) {
		return refuse("pipes", "")
	}
	return t, nil
}

// grantTTL is how long an admitted pipe is held against the limit while it
// does not show up in its session, as when its join then fails.
const grantTTL = 5 * time.Second

// pipeGrant is a pipe admitted past client_max_pipes that its session does not
// count yet: a first pipe until its session registers, a join until zmux has
// attached it.
type pipeGrant struct {
	t    *ticket
	zc   *zmux.Conn // nil for a first pipe
	pipe uint64
	at   time.Time
}

// admitFirstPipe checks the limit for the first pipe of a new session (§13).
// The sessions it will replace, of the same instance, do not count.
func (a *Acceptor) admitFirstPipe(t *ticket) bool {
	if a.maxPipes <= 0 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pipesLocked(t) >= a.maxPipes {
		return false
	}
	a.grants[t.client] = append(a.grants[t.client], &pipeGrant{t: t, at: time.Now()})
	return true
}

// authorizeJoin is zmux.Config.AuthorizeJoin: the limit for a further pipe.
func (a *Acceptor) authorizeJoin(zc *zmux.Conn, pipe uint64) bool {
	if a.maxPipes <= 0 {
		return true
	}
	peer, ok := zc.Admitted().(*seal.Peer)
	if !ok {
		return false
	}
	t, ok := peer.Value.(*ticket)
	if !ok {
		return false
	}
	a.mu.Lock()
	if a.pipesLocked(t) < a.maxPipes {
		a.grants[t.client] = append(a.grants[t.client], &pipeGrant{t: t, zc: zc, pipe: pipe, at: time.Now()})
		a.mu.Unlock()
		return true
	}
	a.mu.Unlock()
	a.authFailed(coreapi.AuthFailure{Source: t.src, Client: t.client, Reason: "pipes"})
	return false
}

// pipesLocked counts the pipes of t's client against the limit: those of its
// sessions and the grants still pending, leaving out the sessions of t's own
// instance other than the one t belongs to (being taken over).
func (a *Acceptor) pipesLocked(t *ticket) int {
	n := 0
	for _, s := range a.sess[t.client] {
		if s.t == t || !bytes.Equal(s.t.static, t.static) {
			n += len(s.zc.Pipes())
		}
	}
	now := time.Now()
	kept := a.grants[t.client][:0]
	for _, g := range a.grants[t.client] {
		attached := false
		if g.zc != nil {
			_, attached = g.zc.Pipe(g.pipe)
		}
		if attached || now.Sub(g.at) >= grantTTL {
			continue
		}
		kept = append(kept, g)
		n++
	}
	if len(kept) == 0 {
		delete(a.grants, t.client)
	} else {
		a.grants[t.client] = kept
	}
	return n
}

// dropGrant ends the grant of a first pipe once its session counts it.
func (a *Acceptor) dropGrant(t *ticket) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.grants[t.client]
	for i, g := range l {
		if g.t == t && g.zc == nil {
			a.grants[t.client] = append(l[:i:i], l[i+1:]...)
			return
		}
	}
}

// acceptAnyClient is the ClientAnchor: psk0 has proved the vkey by msg3, and
// without an Anchor msg3's data would stay unauthenticated (§3.4).
type acceptAnyClient struct{}

func (acceptAnyClient) Verify(context.Context, seal.VerifyInput) (seal.Verdict, error) {
	return seal.Verdict{}, nil
}

// allowEstablishMode is ServerConfig.AllowRecordMode. Per-client Trusted is in
// Commit; a carrier that cannot do Bound was refused at msg1 by zmux (§3.2).
func allowEstablishMode(_ zmux.CarrierInfo, m zmux.RecordMode, _ any) bool {
	return m == zmux.Sealed || m == zmux.Bound || m == zmux.Trusted
}

// allowJoinMode is Config.AllowRecordMode: only a client with its switch off
// joins Trusted.
func (a *Acceptor) allowJoinMode(_ zmux.CarrierInfo, m zmux.RecordMode, peer any) bool {
	switch m {
	case zmux.Sealed, zmux.Bound:
		return true
	case zmux.Trusted:
		p, ok := peer.(*seal.Peer)
		if !ok {
			return false
		}
		t, ok := p.Value.(*ticket)
		if !ok {
			return false
		}
		e := a.table.get(t.client)
		return e != nil && !e.Crypt
	}
	return false
}

// onReject is §3.2: a client trying Bound or Trusted learns at once and falls
// back; everything else, Join included, gets silence.
func onReject(info zmux.RejectInfo) zmux.RejectAction {
	log.Debug("carrier rejected", "src_ip", info.Src.Addr().Unmap().String(), "reason", info.Reason)
	if info.Mode == zmux.Bound || info.Mode == zmux.Trusted {
		return zmux.RejectClose
	}
	return zmux.RejectStall
}
