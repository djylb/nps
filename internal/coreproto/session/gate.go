package session

import (
	"context"
	"errors"
	"net/netip"

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
	crypt   bool
	hello   wire.Hello
	static  []byte
	src     netip.AddrPort
	carrier string // set after the handshake (admitPipe)
}

// gate is the seal.Gate of the entry.
type gate struct{ a *Acceptor }

func (g *gate) Resolve(_ context.Context, handle []byte, _ netip.AddrPort) ([]byte, error) {
	if e := g.a.table.lookup(handle); e != nil {
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
	return &ticket{client: e.ID, crypt: e.Crypt, hello: hello, static: a.Peer.Static, src: a.Source}, nil
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
