package session

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/slogx"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxws"
)

var log = slogx.Module("core")

// legacyMagic starts a carrier of the old protocol.
const legacyMagic = "TST"

// legacySubprotocol is the WebSocket subprotocol that, on the bridge path,
// marks the core protocol while the old protocol still listens there (§12).
const legacySubprotocol = "binary"

const (
	drainTimeout = 5 * time.Second
	// Shared bridge ports see many short carriers of old clients per source.
	maxPending          = 4096
	maxPendingPerSource = 64
)

type carrierKey struct{}

// carrierCtx is what the fallback needs of its carrier.
type carrierCtx struct {
	label string
	tls   *tls.Conn
}

// Acceptor is the core protocol entry of the server's bridge ports.
type Acceptor struct {
	table *handleTable
	zl    *zmux.Listener
	hooks atomic.Pointer[coreapi.Hooks]
	key   *ecdh.PrivateKey
	hello []byte

	mu   sync.Mutex
	sess map[int][]*serverSession

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewAcceptor builds the entry over store, with a key of this process
// (nothing is persisted, §3.3).
func NewAcceptor(store Store) (*Acceptor, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	a := &Acceptor{table: newHandleTable(store), key: key, hello: serverHello(), sess: map[int][]*serverSession{}}
	a.zl, err = zmux.NewListener(&zmux.ListenerConfig{
		Config:              &zmux.Config{AllowRecordMode: a.allowJoinMode},
		MaxPending:          maxPending,
		MaxPendingPerSource: maxPendingPerSource,
		Admission: &zmux.Admission{
			Peek: 1, MaxPeek: wire.MaxPeek, FallbackOnPeekTimeout: true, Decide: a.decide,
		},
		OnReject: onReject,
		Fallback: a.fallback,
		WrapPipe: &admitPipe{a: a, cfg: &seal.ServerConfig{
			Gate:            &gate{a},
			Key:             key,
			ClientAnchor:    acceptAnyClient{},
			AppData:         a.appData,
			MaxAppData:      wire.MaxHelloRecv,
			AllowRecordMode: allowEstablishMode,
		}},
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// admitPipe runs Establish, names the carrier on the ticket and reports the
// failure of a client the admission identified by its Handle.
type admitPipe struct {
	a   *Acceptor
	cfg *seal.ServerConfig
}

// Validate implements the check of zmux.NewListener.
func (w *admitPipe) Validate() error { return w.cfg.Validate() }

// WrapPipe implements zmux.PipeWrapper.
func (w *admitPipe) WrapPipe(ctx context.Context, p zmux.PipeIO, hint zmux.PipeHint) (zmux.PipeIO, error) {
	q, err := w.cfg.WrapPipe(ctx, p, hint)
	if err != nil {
		w.report(err, p, hint)
		return q, err
	}
	if peer, ok := seal.PeerOf(q); ok {
		if t, ok := peer.Value.(*ticket); ok {
			t.carrier = carrierLabel(p)
		}
	}
	return q, nil
}

// carrierLabel names a carrier for the log; the HTTP adapters give no label.
func carrierLabel(p zmux.PipeIO) string {
	switch _, ok := zmux.TerminatedTLS(p); {
	case p.Label != "":
		return p.Label
	case ok:
		return "wss"
	}
	return "ws"
}

func (w *admitPipe) report(err error, p zmux.PipeIO, hint zmux.PipeHint) {
	var re *zmux.RejectError
	e, known := hint.Admitted.(*clientEntry)
	if !errors.As(err, &re) || !known {
		return
	}
	// A failing Bound is how a TLS-terminating intermediary shows up
	// (§3.2): the client retries Sealed, and it is no attack.
	if re.Mode == zmux.Bound {
		return
	}
	var reason string
	switch re.Reason {
	case "auth":
		reason = "bad_key"
	case "malformed", "bad_flags", "trailing_bytes", "tlv_truncated", "tlv_non_minimal", "tlv_order", "tlv_unknown_critical", "too_large":
		reason = "malformed"
	default:
		return
	}
	w.a.authFailed(coreapi.AuthFailure{Source: hint.Source, Carrier: carrierLabel(p), Client: e.ID, Reason: reason})
}

func (a *Acceptor) appData(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
	if r.Message == 2 {
		return a.hello, nil
	}
	return nil, nil
}

// Start implements coreapi.Entry.
func (a *Acceptor) Start(ctx context.Context, h coreapi.Hooks) error {
	a.hooks.Store(&h)
	ctx, a.cancel = context.WithCancel(ctx)
	id := seal.NodeKeyID(a.key.PublicKey().Bytes())
	log.Info("core protocol entry ready", "key_id", hex.EncodeToString(id[:]))
	a.wg.Go(func() {
		for {
			zc, err := a.zl.Accept(ctx)
			if err != nil {
				return
			}
			a.wg.Go(func() { a.handle(ctx, zc) })
		}
	})
	return nil
}

// Close implements coreapi.Entry.
func (a *Acceptor) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	_ = a.zl.Close()
	a.mu.Lock()
	var all []*serverSession
	for _, l := range a.sess {
		all = append(all, l...)
	}
	a.mu.Unlock()
	for _, s := range all {
		_ = s.zc.Close()
	}
	a.wg.Wait()
	return nil
}

// Serve implements coreapi.Entry.
func (a *Acceptor) Serve(ctx context.Context, c net.Conn, carrier string) {
	var p zmux.PipeIO
	info := carrierCtx{label: carrier}
	if tc, ok := c.(*tls.Conn); ok {
		// A connection this process accepted itself: end to end.
		p, info.tls = zmux.SecureTLS(tc), tc
	} else {
		p = zmux.NetPipe(c)
	}
	p.Label = carrier
	ctx = context.WithValue(ctx, carrierKey{}, info)
	_ = a.zl.HandlePipe(ctx, p, zmux.PipeHint{Source: addrPort(c.RemoteAddr())})
}

// WebSocket implements coreapi.Entry (§12): on the bridge path the subprotocol
// binary is the core protocol; anything else stays with the old one.
func (a *Acceptor) WebSocket(legacy http.Handler) (http.Handler, error) {
	return zmuxws.Handler(a.zl, &zmux.HTTPOptions{Next: legacy}, legacySubprotocol)
}

// WebSocketAnyProtocol implements coreapi.Entry (§12): the path is the core
// protocol's, so a request is accepted with the subprotocol it offered (the
// first of them) or with none.
func (a *Acceptor) WebSocketAnyProtocol(next http.Handler) (http.Handler, error) {
	o := &zmux.HTTPOptions{Next: next}
	if _, err := zmuxws.Handler(a.zl, o); err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocols := []string{zmuxws.NoSubprotocol}
		for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
			for p := range strings.SplitSeq(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					protocols = append(protocols, p)
				}
			}
		}
		// Built per request: it holds no state, and the protocols are the
		// request's own, which is what accepting any subprotocol means.
		h, err := zmuxws.Handler(a.zl, o, protocols...)
		if err != nil {
			o.Fallback(w, r)
			return
		}
		h.ServeHTTP(w, r)
	}), nil
}

// decide routes a carrier by its first bytes (§12, §13). It is the one place
// that does, for every entry point of the listener.
func (a *Acceptor) decide(_ context.Context, info *zmux.CarrierInfo, prefix []byte) zmux.Decision {
	if info.Head {
		// The HTTP handler matched the path and subprotocol.
		return zmux.Decision{Verdict: zmux.VerdictAccept}
	}
	if len(prefix) == 0 {
		return zmux.Decision{Verdict: zmux.VerdictFallback}
	}
	switch b := prefix[0]; b {
	case 2:
		known, need := a.zl.KnownJoin(prefix)
		if need > 0 {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: need}
		}
		if known {
			return zmux.Decision{Verdict: zmux.VerdictAccept}
		}
	case 1, 3:
		if len(prefix) < wire.HelloHeadLen {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: wire.HelloHeadLen}
		}
		if e := a.table.lookup(prefix[2:wire.HelloHeadLen]); e != nil {
			return zmux.Decision{Verdict: zmux.VerdictAccept, Value: e}
		}
		log.Debug("unknown handle", "src_ip", info.Source.Addr().Unmap().String())
	case legacyMagic[0]:
		if len(prefix) < len(legacyMagic) {
			return zmux.Decision{Verdict: zmux.VerdictMore, Need: len(legacyMagic)}
		}
	}
	return zmux.Decision{Verdict: zmux.VerdictFallback}
}

// fallback takes what the admission did not: TST goes to the old protocol,
// anything else to the domain proxy, or stalls when there is none. It owns c.
func (a *Acceptor) fallback(ctx context.Context, c net.Conn) {
	head := make([]byte, len(legacyMagic))
	// The prefix is replayed at once; the deadline matters only for a
	// carrier that took the peek timeout with fewer bytes.
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, _ := io.ReadFull(c, head)
	_ = c.SetReadDeadline(time.Time{})
	nc := zmux.PrefixedConn(c, head[:n])
	info, _ := ctx.Value(carrierKey{}).(carrierCtx)
	h := a.hooks.Load()
	if h != nil && n == len(head) && string(head) == legacyMagic && h.Legacy != nil {
		h.Legacy(nc, info.label)
		return
	}
	if h != nil && h.Fallback != nil {
		var state *tls.ConnectionState
		if info.tls != nil {
			st := info.tls.ConnectionState()
			state = &st
		}
		if h.Fallback(nc, state) {
			return
		}
	}
	t := time.NewTimer(a.zl.StallDuration(addrPort(c.RemoteAddr())))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	_ = nc.Close()
}

func addrPort(a net.Addr) netip.AddrPort {
	if a == nil {
		return netip.AddrPort{}
	}
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}

func (a *Acceptor) authFailed(f coreapi.AuthFailure) {
	if h := a.hooks.Load(); h != nil && h.AuthFailed != nil {
		h.AuthFailed(f)
	}
}

// handle runs an established session until it ends.
func (a *Acceptor) handle(ctx context.Context, zc *zmux.Conn) {
	peer, ok := zc.Admitted().(*seal.Peer)
	if !ok {
		_ = zc.Close()
		return
	}
	t, ok := peer.Value.(*ticket)
	if !ok {
		_ = zc.Close()
		return
	}
	s := &serverSession{zc: zc, t: t, mode: peer.Mode, since: time.Now()}
	replaced := a.register(s)
	h := a.hooks.Load()
	for _, old := range replaced {
		old.closing.Store("replaced")
		if h != nil && h.Takeover != nil {
			h.Takeover(s, old)
		}
		a.drain(old)
	}
	if h != nil && h.Online != nil {
		h.Online(s)
	}
	// The client may have changed between Commit and register.
	if e := a.table.get(t.client); e == nil || !e.Enabled {
		s.closeAuth("deleted")
	} else if e.Crypt && s.mode == zmux.Trusted {
		a.drain(s)
	}
	a.wg.Go(func() { rejectStreams(ctx, zc, wire.Op.FromClient) })
	a.wg.Go(func() { rejectUniStreams(ctx, zc, wire.Op.FromClient) })
	if err := s.pushConfig(a.snapshotOf(t.client)); err != nil {
		log.Debug("config push failed", "client", t.client, "err", err)
	}
	_ = zc.Wait()
	a.unregister(s)
	if h != nil && h.Offline != nil {
		reason, _ := s.closing.Load().(string)
		if reason == "" {
			reason = "closed"
		}
		h.Offline(s, reason)
	}
}

// snapshotOf is what CONFIG carries so far: the record rule of §3.2.
func (a *Acceptor) snapshotOf(client int) *wire.Snapshot {
	if e := a.table.get(client); e != nil && !e.Crypt {
		return &wire.Snapshot{Record: wire.RecordTrusted}
	}
	return &wire.Snapshot{}
}

// register adds s and returns the sessions of the same instance it replaces
// (§3.6); sessions of other instances are left alone.
func (a *Acceptor) register(s *serverSession) (replaced []*serverSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.sess[s.t.client][:0:0]
	for _, o := range a.sess[s.t.client] {
		if bytes.Equal(o.t.static, s.t.static) {
			replaced = append(replaced, o)
		} else {
			kept = append(kept, o)
		}
	}
	a.sess[s.t.client] = append(kept, s)
	return replaced
}

func (a *Acceptor) unregister(s *serverSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.sess[s.t.client]
	for i, o := range l {
		if o == s {
			l = append(l[:i:i], l[i+1:]...)
			break
		}
	}
	if len(l) == 0 {
		delete(a.sess, s.t.client)
	} else {
		a.sess[s.t.client] = l
	}
}

func (a *Acceptor) sessionsOf(id int) []*serverSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*serverSession(nil), a.sess[id]...)
}

// drain ends s with NPS_DRAIN in the background.
func (a *Acceptor) drain(s *serverSession) {
	a.wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		_ = s.zc.Shutdown(ctx, zmux.ApplicationErrorCode(wire.CodeDrain), "")
	})
}

// ClientChanged implements coreapi.Entry: §3.2's rules for the switch, §3.7
// for a client that may no longer connect.
func (a *Acceptor) ClientChanged(r coreapi.ClientRecord) {
	old := a.table.set(r)
	sessions := a.sessionsOf(r.ID)
	switch {
	case !r.Enabled || old != nil && old.VKey != r.VKey:
		for _, s := range sessions {
			s.closeAuth("deleted")
		}
	case old == nil:
	case !old.Crypt && r.Crypt:
		for _, s := range sessions {
			a.drain(s)
		}
	case old.Crypt && !r.Crypt:
		for _, s := range sessions {
			a.wg.Go(func() {
				if err := s.pushConfig(a.snapshotOf(r.ID)); err != nil {
					log.Debug("config push failed", "client", r.ID, "err", err)
				}
			})
		}
	}
}

// ClientRemoved implements coreapi.Entry.
func (a *Acceptor) ClientRemoved(id int) {
	a.table.remove(id)
	for _, s := range a.sessionsOf(id) {
		s.closeAuth("deleted")
	}
}

// Kick implements coreapi.Entry.
func (a *Acceptor) Kick(id int) {
	for _, s := range a.sessionsOf(id) {
		s.closing.Store("deleted")
		a.drain(s)
	}
}

// serverSession implements coreapi.Session.
type serverSession struct {
	zc      *zmux.Conn
	t       *ticket
	mode    zmux.RecordMode
	since   time.Time
	seq     atomic.Uint64
	pushMu  sync.Mutex
	closing atomic.Value // string: reason of a close this side made
}

func (s *serverSession) ClientID() int          { return s.t.client }
func (s *serverSession) Instance() string       { return string(s.t.static) }
func (s *serverSession) Source() netip.AddrPort { return s.t.src }
func (s *serverSession) Carrier() string        { return s.t.carrier }
func (s *serverSession) Agent() string          { return s.t.hello.Agent }
func (s *serverSession) Since() time.Time       { return s.since }

func (s *serverSession) closeAuth(reason string) {
	s.closing.Store(reason)
	_ = s.zc.CloseWithError(zmux.ApplicationErrorCode(wire.CodeAuth), "")
}

// pushConfig opens one stream carrying a full snapshot (§4.3). Pushes are
// serialized so that seq order is the order of the snapshots' content.
func (s *serverSession) pushConfig(snap *wire.Snapshot) error {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	st, err := s.zc.OpenUniStream()
	if err != nil {
		return err
	}
	if err := st.SetPreamble(wire.AppendConfigOpen(s.seq.Add(1))); err != nil {
		st.CancelWrite(zmux.StreamErrorCode(wire.CodeInternal))
		return err
	}
	if body := wire.AppendSnapshot(nil, snap); len(body) > 0 {
		if _, err := st.Write(body); err != nil {
			return err
		}
	}
	return st.Close()
}
