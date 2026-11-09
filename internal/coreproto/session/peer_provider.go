package session

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/acl"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/zmuxquic"
)

const (
	linkDomain     = "nps link"
	nestedResume   = 30 * time.Second
	nestedMaxPipes = 8
	// nestedFirstPipe is how long a provider waits for the first pipe of a
	// link it accepted.
	nestedFirstPipe = time.Minute
)

// nestedConfig is the zmux configuration both ends of a link use (§9.3).
func nestedConfig(maxLanes int) *zmux.Config {
	return &zmux.Config{EnableDatagrams: true, MaxPipes: nestedMaxPipes, ResumeTimeout: nestedResume, Scheduler: zmux.SpreadETA(),
		AllowRecordMode: allowNestedMode, RecordPolicy: nestedMode, MaxLanes: maxLanesOf(maxLanes)}
}

// DefaultMaxLanes is the lanes accepted per pipe: more bring next to nothing.
const DefaultMaxLanes = 16

// maxLanesOf is session_max_lanes as zmux takes it, whose own zero is 258.
func maxLanesOf(n int) int {
	if n <= 0 {
		return DefaultMaxLanes
	}
	return n
}

// allowNestedMode is Sealed, or Bound on a carrier that secures itself end to
// end: never on a relay, whose server must not see the records (§9.3).
func allowNestedMode(info zmux.CarrierInfo, m zmux.RecordMode, _ any) bool {
	return m == zmux.Sealed || m == zmux.Bound && info.Secure
}

// nestedMode is the mode a nested pipe asks for: Bound on a direct QUIC path
// (which also gives it lanes), Sealed otherwise.
func nestedMode(info zmux.CarrierInfo) zmux.RecordMode {
	if info.Secure {
		return zmux.Bound
	}
	return zmux.Sealed
}

// relayPipe hides the lanes of an outer stream: an extra lane would be a
// stream the server cannot route (§9.3). It must never declare CarrierSecure:
// the server is on the path of a relay.
func relayPipe(p zmux.PipeIO) zmux.PipeIO {
	p.W = &relayWriter{w: p.W}
	return p
}

type relayWriter struct{ w io.WriteCloser }

func (r *relayWriter) Write(b []byte) (int, error)          { return r.w.Write(b) }
func (r *relayWriter) WriteBuffers(b [][]byte) (int, error) { return zmux.WriteFlat(r.w, b) }
func (r *relayWriter) Close() error                         { return r.w.Close() }
func (r *relayWriter) Abort(err error) {
	if a, ok := r.w.(interface{ Abort(error) }); ok {
		a.Abort(err)
	}
}
func (r *relayWriter) CarrierConn() any {
	if c, ok := r.w.(interface{ CarrierConn() any }); ok {
		return c.CarrierConn()
	}
	return nil
}

// linkGate is the seal.Gate of one link: its one handle.
type linkGate struct{ handle, psk []byte }

func (g *linkGate) Resolve(_ context.Context, h []byte, _ netip.AddrPort) ([]byte, error) {
	if subtle.ConstantTimeCompare(h, g.handle) != 1 {
		return nil, errUnknownHandle
	}
	return g.psk, nil
}
func (g *linkGate) Credential(context.Context, seal.CredentialRequest) (*seal.Credential, error) {
	return nil, nil
}
func (g *linkGate) Commit(context.Context, seal.Admission) (any, error) { return g, nil }

// providerLink is a provider's end of one link.
type providerLink struct {
	s     *clientSession
	id    uint64
	zl    *zmux.Listener
	exit  coreapi.Exit
	shape *nestedShaping
	// meter counts the bytes of the link's direct pipes (§9.6); reported is
	// what the server was last told, under usageMu.
	meter    directMeter
	usageMu  sync.Mutex
	reported usageReport
	// ctx ends the nested session (revocation); paths outlive it on pctx,
	// so that the close reaches the visitor over them.
	ctx     context.Context
	cancel  context.CancelFunc
	pctx    context.Context
	pcancel context.CancelFunc
}

func (s *clientSession) nestedKey() (*ecdh.PrivateKey, error) {
	if s.d.cfg.Key != nil {
		return s.d.cfg.Key, nil
	}
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// serveProviderLink accepts a LINK from the server and holds it for the life
// of the nested session (§9.1).
func (s *clientSession) serveProviderLink(ctx context.Context, lc *linkConn, fields []byte) {
	o, err := wire.ParsePathOpen(wire.OpLink, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	_ = lc.SetReadDeadline(time.Now().Add(linkOpenTimeoutProvider))
	n, err := wire.ReadVarintFrom(lc)
	if err != nil || n > wire.MaxLinkBody {
		lc.abort(wire.CodeMalformed)
		return
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(lc, buf); err != nil {
		lc.abort(wire.CodeMalformed)
		return
	}
	body, err := wire.ParseLinkBody(buf)
	if err != nil {
		failLink(lc, true, wire.ErrorCode(err))
		return
	}
	_ = lc.SetReadDeadline(time.Time{})
	if s.d.cfg.PeerExit == nil {
		failLink(lc, true, wire.CodeNotAllowed)
		return
	}
	exit, err := s.d.cfg.PeerExit(policyOfBody(&body))
	if err != nil {
		code := wire.CodeUnsupported
		if errors.Is(err, acl.ErrMalformedScope) {
			code = wire.CodeMalformed
		}
		failLink(lc, true, code)
		return
	}
	p, err := s.newProviderLink(ctx, o.LinkID, &body, exit)
	if err != nil {
		failLink(lc, true, wire.CodeInternal)
		return
	}
	s.addProviderLink(p)
	defer s.dropProviderLink(p)
	defer p.close()
	if !ackLink(lc, &wire.StreamOpen{WantReply: true}) {
		return
	}
	go func() {
		_, _ = io.Copy(io.Discard, lc)
		p.cancel() // the server revoked the link, or the session ended
	}()
	p.run(lc)
}

const linkOpenTimeoutProvider = 5 * time.Second

func policyOfBody(b *wire.LinkBody) coreapi.LinkPolicy {
	p := coreapi.LinkPolicy{NameDest: b.NameDest, Scope: b.Scope}
	switch b.TargetType {
	case wire.TargetTCP:
		p.TargetType = coreapi.NetworkTCP
	case wire.TargetUDP:
		p.TargetType = coreapi.NetworkUDP
	}
	for _, a := range b.Targets {
		p.Targets = append(p.Targets, a.String())
	}
	return p
}

func (s *clientSession) newProviderLink(ctx context.Context, id uint64, b *wire.LinkBody, exit coreapi.Exit) (*providerLink, error) {
	handle, psk, err := seal.DeriveHandlePSK(b.LinkKey, []byte(linkDomain), nil)
	if err != nil {
		return nil, err
	}
	key, err := s.nestedKey()
	if err != nil {
		return nil, err
	}
	shape := s.d.nestedShaping()
	base := nestedConfig(s.d.cfg.MaxLanes)
	zl, err := zmux.NewListener(&zmux.ListenerConfig{Config: base, WrapPipe: &seal.ServerConfig{
		Shaper: shape.slot.handshake,
		Gate:   &linkGate{handle: handle, psk: psk}, Key: key, ClientAnchor: acceptAnyClient{},
		AllowRecordMode: allowNestedMode, MaxAppData: wire.MaxHelloRecv, AppData: nestedProviderHello,
	}, SessionConfig: func(context.Context, *zmux.CarrierInfo, any) (*zmux.Config, error) {
		return shape.configure(base.Clone()), nil
	}})
	if err != nil {
		return nil, err
	}
	lctx, cancel := context.WithCancel(ctx)
	pctx, pcancel := context.WithCancel(context.WithoutCancel(ctx))
	return &providerLink{s: s, id: id, zl: zl, exit: exit, shape: shape, ctx: lctx, cancel: cancel, pctx: pctx, pcancel: pcancel}, nil
}

// nestedProviderHello is msg2 of a nested handshake: what the provider can
// decompress (§7.2, §9.3).
func nestedProviderHello(_ context.Context, r seal.AppDataRequest) ([]byte, error) {
	if r.Message == 2 {
		return wire.AppendHello(nil, &wire.Hello{CompressAlgs: compressAlgs}), nil
	}
	return nil, nil
}

func (p *providerLink) close() {
	p.cancel()
	p.pcancel()
	_ = p.zl.Close()
}

// run serves the nested session until it ends, then closes the LINK (§9.1).
func (p *providerLink) run(lc *linkConn) {
	defer lc.Close()
	actx, cancel := context.WithTimeout(p.ctx, nestedFirstPipe)
	zc, err := p.zl.Accept(actx)
	cancel()
	if err != nil {
		return
	}
	defer zc.Close()
	// The last report goes before the nested session and the LINK close.
	defer p.finalUsage(zc)
	p.shape.attach(zc)
	defer p.shape.detach()
	stop := context.AfterFunc(p.ctx, func() {
		p.sampleUsage(zc) // a revoked link's pipes end with the close
		_ = zc.Close()
	})
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { p.runUsage(zc) })
	wg.Go(func() {
		for {
			st, err := zc.AcceptStream(p.ctx)
			if err != nil {
				return
			}
			wg.Go(func() {
				p.serveNested(st, zc)
				p.sampleUsage(zc)
			})
		}
	})
	_ = zc.Wait()
	p.cancel()
}

// serveNested is one TCP or UDP stream of the visitor, under the link's
// policy (§9.3).
func (p *providerLink) serveNested(st *zmux.Stream, zc *zmux.Conn) {
	lc := &linkConn{Stream: st, local: zc.LocalAddr(), remote: zc.RemoteAddr()}
	op, fields, err := wire.ParseOpenOp(st.Preamble())
	if err != nil || op != wire.OpTCP && op != wire.OpUDP {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	o, err := wire.ParseStreamOpen(op, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	if o.LinkID != 0 || len(o.Service) > 0 || o.Compress != 0 && !localCompress(o.Compress) {
		failLink(lc, o.WantReply, wire.CodeUnsupported)
		return
	}
	serveExitLink(p.ctx, p.exit, zc, p.s.d.limits, lc, &o)
}

// servePipe takes a relay PIPE of the link.
func (s *clientSession) servePipe(ctx context.Context, st *zmux.Stream, lc *linkConn, fields []byte) {
	o, err := wire.ParsePathOpen(wire.OpPipe, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	p := s.providerLinkOf(o.LinkID)
	if p == nil {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	pipe := zmux.StreamPipe(st)
	_ = p.zl.HandlePipe(p.pctx, relayPipe(pipe), zmux.PipeHint{})
}

// servePunch runs a PUNCH of the link and takes the direct path it makes.
func (s *clientSession) servePunch(ctx context.Context, lc *linkConn, fields []byte) {
	o, err := wire.ParsePathOpen(wire.OpPunch, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	p := s.providerLinkOf(o.LinkID)
	if p == nil || s.d.cfg.Puncher == nil {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	path, err := s.d.cfg.Puncher.Punch(p.pctx, lc, o.Background, true)
	_ = lc.Close()
	if err != nil {
		log.Debug("punch failed", "role", "provider", "err", err)
		return
	}
	defer path.Close()
	_ = zmuxquic.ServeConn(p.pctx, path.QUIC, p.zl, func(_ context.Context, _ *quic.Conn, c zmux.PipeIO) {
		_ = zmux.PipeConn(c).Close()
	})
}

func (s *clientSession) addProviderLink(p *providerLink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provided == nil {
		s.provided = map[uint64]*providerLink{}
	}
	s.provided[p.id] = p
}

func (s *clientSession) dropProviderLink(p *providerLink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.provided, p.id)
}

// providing reports whether the session is the provider of a live link.
func (s *clientSession) providing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.provided) > 0
}

func (s *clientSession) providerLinkOf(id uint64) *providerLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provided[id]
}
