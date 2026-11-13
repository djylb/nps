package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// linkOpenTimeout bounds the provider's answer to a LINK (§9.1) and opening
// the provider's end of a PIPE or PUNCH.
const linkOpenTimeout = 5 * time.Second

// linkPathGrace is how long the paths of an ended link stay up.
const linkPathGrace = 2 * time.Second

// peerLink is the server's record of one link. Its anchor is the provider's
// LINK stream, or the visitor's with server_exit (§9.1): the record lives
// exactly as long as the anchor.
type peerLink struct {
	id       uint64
	visitor  int
	grant    *coreapi.LinkGrant
	anchor   *linkConn
	provider *serverSession // nil with server_exit

	mu    sync.Mutex
	ended bool
	paths map[wire.Op]*linkPath  // at most one PIPE and one PUNCH (§4.6)
	exits map[*linkConn]struct{} // active streams of a server-exit link

	// relayChange wakes the visitor's relay watch (KeyRelayWatch).
	relayChange chan struct{}
}

// relayChanged wakes the relay watch of l, if any.
func (l *peerLink) relayChanged() {
	select {
	case l.relayChange <- struct{}{}:
	default:
	}
}

// linkPath is the visitor's end of a PIPE or PUNCH and who opened it.
type linkPath struct {
	s     *serverSession
	abort uint64

	mu      sync.Mutex
	stopped bool
	ends    []*linkConn
}

// stop aborts the path's ends, and any added later.
func (p *linkPath) stop() {
	p.mu.Lock()
	p.stopped = true
	ends := p.ends
	p.ends = nil
	p.mu.Unlock()
	for _, c := range ends {
		c.abort(p.abort)
	}
}

// add makes c an end of the path; a path already stopped aborts it, false.
func (p *linkPath) add(c *linkConn) bool {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		c.abort(p.abort)
		return false
	}
	p.ends = append(p.ends, c)
	p.mu.Unlock()
	return true
}

// acquire makes op's slot p's. A path of an earlier session of the visitor
// gives way; one of the same session keeps the slot.
func (l *peerLink) acquire(op wire.Op, p *linkPath) bool {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return false
	}
	old := l.paths[op]
	if old != nil && old.s == p.s {
		l.mu.Unlock()
		return false
	}
	l.paths[op] = p
	l.mu.Unlock()
	if old != nil {
		old.abort = wire.CodeInternal // replaced, the link lives on
		old.stop()
	}
	return true
}

func (l *peerLink) release(op wire.Op, p *linkPath) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.paths[op] == p {
		delete(l.paths, op)
	}
}

// addExit makes c a stream the link's end aborts; false when it has ended.
func (l *peerLink) addExit(c *linkConn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return false
	}
	l.exits[c] = struct{}{}
	return true
}

func (l *peerLink) dropExit(c *linkConn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.exits, c)
}

// setAnchor records the link's anchor; false when the link was already
// revoked, while its provider stream was still being opened.
func (l *peerLink) setAnchor(c *linkConn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return false
	}
	l.anchor = c
	return true
}

// end stops the paths of the link and resets its anchor.
func (l *peerLink) end(code uint64) {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return
	}
	l.ended = true
	paths := l.paths
	l.paths = nil
	anchor := l.anchor
	exits := l.exits
	l.exits = nil
	l.mu.Unlock()
	if anchor != nil {
		anchor.abort(code)
	}
	for c := range exits {
		c.abort(wire.CodeNotAllowed)
	}
	// The provider closes the nested session over the paths first, so the
	// visitor learns at once; then they end whatever it does.
	time.AfterFunc(linkPathGrace, func() {
		for _, p := range paths {
			p.stop()
		}
	})
}

func (a *Acceptor) newLink(l *peerLink) bool {
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	for range 8 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return false
		}
		id := binary.BigEndian.Uint64(b[:]) >> 2
		if id != 0 && a.links[id] == nil {
			l.id = id
			a.links[id] = l
			return true
		}
	}
	return false
}

func (a *Acceptor) dropLink(l *peerLink) {
	a.linkMu.Lock()
	if a.links[l.id] == l {
		delete(a.links, l.id)
	}
	a.linkMu.Unlock()
	a.endUsage(l.id)
}

func (a *Acceptor) linkOf(id uint64) *peerLink {
	a.linkMu.Lock()
	defer a.linkMu.Unlock()
	return a.links[id]
}

// RevokeLinks implements coreapi.Entry.
func (a *Acceptor) RevokeLinks(match func(tunnel, visitor, provider int) bool) {
	a.linkMu.Lock()
	var hit []*peerLink
	for _, l := range a.links {
		if match(l.grant.Tunnel, l.visitor, l.grant.Provider) {
			hit = append(hit, l)
		}
	}
	a.linkMu.Unlock()
	for _, l := range hit {
		l.end(wire.CodeNotAllowed)
		a.dropLink(l)
	}
}

// providerOf is the live session of a provider, its newest not suspended,
// else its newest.
func (a *Acceptor) providerOf(id int) *serverSession {
	all := a.sessionsOf(id)
	var best *serverSession
	for _, s := range all {
		// A client that did not announce link gets none (§2.3), and a pure
		// visitor provides nothing.
		if s.Closed() || s.visitorOnly || !slices.Contains(s.t.hello.Caps, wire.CapLink) {
			continue
		}
		if best == nil || !s.Suspended() {
			best = s
		}
	}
	return best
}

// serveStreams serves what a client opens.
func (s *serverSession) serveStreams(ctx context.Context) {
	for {
		st, err := s.zc.AcceptStream(ctx)
		if err != nil {
			return
		}
		s.a.wg.Go(func() { s.serveStream(ctx, st) })
	}
}

func (s *serverSession) conn(st *zmux.Stream) *linkConn {
	return &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: net.TCPAddrFromAddrPort(s.t.src)}
}

func (s *serverSession) serveStream(ctx context.Context, st *zmux.Stream) {
	lc := s.conn(st)
	op, fields, err := wire.ParseOpenOp(st.Preamble())
	if err != nil {
		lc.abort(wire.CodeMalformed)
		return
	}
	if s.configOnly && op != wire.OpConfigUpload && op != wire.OpRegister {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	if s.visitorKey && (op == wire.OpConfigUpload || op == wire.OpRegister) {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	// What only a provider reports.
	if s.visitorOnly && (op == wire.OpHealth || op == wire.OpP2PUsage || op == wire.OpService) {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	switch op {
	case wire.OpLink:
		s.serveLinkOpen(ctx, lc, fields)
	case wire.OpPipe, wire.OpPunch:
		s.servePath(ctx, lc, op, fields)
	case wire.OpTCP, wire.OpUDP:
		s.serveExitStream(ctx, lc, op, fields)
	case wire.OpHealth:
		s.serveHealth(lc)
	case wire.OpP2PUsage:
		s.serveUsage(lc)
	case wire.OpService:
		s.serveServices(lc, fields)
	case wire.OpConfigUpload:
		s.serveTransition(lc, s.a.hookConfigUpload())
	case wire.OpRegister:
		s.serveTransition(lc, s.a.hookRegister())
	default:
		lc.abort(rejectCode(st.Preamble(), wire.Op.FromClient))
	}
}

func linkFailCode(err error) uint64 {
	var le *coreapi.LinkError
	if errors.As(err, &le) {
		return le.Code
	}
	return wire.CodeNotAllowed
}

// serveLinkOpen answers a visitor's LINK (§9.1).
func (s *serverSession) serveLinkOpen(ctx context.Context, lc *linkConn, fields []byte) {
	o, err := wire.ParseLinkOpen(fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	h := s.a.hooks.Load()
	if h == nil || h.ResolveLink == nil {
		failLink(lc, true, wire.CodeNotAllowed)
		return
	}
	r := coreapi.LinkResolve{Tunnel: int(o.Tunnel), Source: s.t.src, ServerExit: o.ServerExit}
	if len(o.Access) > 0 {
		id, proof, ok := wire.SplitAccess(o.Access)
		if !ok {
			failLink(lc, true, wire.CodeMalformed)
			return
		}
		r.AccessID, r.Verify = id, func(password []byte) bool { return s.verifyAccess(password, proof) }
	}
	g, err := h.ResolveLink(s.ClientID(), r)
	if err != nil {
		failLink(lc, true, linkFailCode(err))
		return
	}
	l := &peerLink{visitor: s.ClientID(), grant: g, paths: map[wire.Op]*linkPath{}, exits: map[*linkConn]struct{}{}, relayChange: make(chan struct{}, 1)}
	if g.ServerExit {
		s.holdExitLink(lc, l)
		return
	}
	noRelay := !relayAllowed(g)
	if noRelay && o.ConnMode == wire.ConnRelay {
		log.Info("p2p relay refused: the tunnel does not allow relaying", "tunnel", g.Tunnel, "client", s.ClientID(), "conn_mode", "relay")
		_ = lc.SetWriteDeadline(time.Now().Add(linkOpenTimeout))
		if _, err := lc.Write(wire.AppendReply(nil, wire.CodeNotAllowed, RelayDisabledReason, nil)); err == nil {
			lc.SetReliableBoundary()
		}
		lc.abort(wire.CodeNotAllowed)
		return
	}
	code, reply := s.a.openProviderLink(ctx, l, o.ConnMode)
	if code != wire.CodeOK {
		failLink(lc, true, code)
		return
	}
	if noRelay {
		// A visitor that does not read NoRelay still takes the direct mode
		// and opens no PIPE.
		reply.NoRelay, reply.ConnMode = true, wire.ConnDirect
	}
	_ = lc.SetWriteDeadline(time.Now().Add(linkOpenTimeout))
	if _, err := lc.Write(wire.AppendReply(nil, wire.CodeOK, "", wire.AppendLinkReply(reply))); err != nil || !o.RelayWatch {
		_ = lc.Close() // one question, one answer: the visitor's side holds nothing
		return
	}
	_ = lc.SetWriteDeadline(time.Time{})
	s.watchRelay(lc, l, noRelay)
}

// watchRelay keeps a visitor's LINK stream that asked for it and sends a
// relay state head whenever the relay permission changes, until the link or
// the stream ends.
func (s *serverSession) watchRelay(lc *linkConn, l *peerLink, sent bool) {
	if !l.addExit(lc) {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	defer l.dropExit(lc)
	gone := make(chan struct{})
	s.a.wg.Go(func() {
		_, _ = io.Copy(io.Discard, lc)
		close(gone)
	})
	if l.grant.WatchRelay != nil {
		defer l.grant.WatchRelay(l.relayChanged)()
	}
	l.relayChanged() // a change between the reply and the watch
	for {
		select {
		case <-gone:
			return
		case <-l.relayChange:
		}
		now := !relayAllowed(l.grant)
		if now == sent {
			continue
		}
		_ = lc.SetWriteDeadline(time.Now().Add(linkOpenTimeout))
		if _, err := lc.Write(wire.AppendReply(nil, wire.CodeOK, "", wire.AppendRelayState(now))); err != nil {
			lc.abort(wire.CodeInternal)
			return
		}
		sent = now
		log.Debug("p2p relay state sent", "tunnel", l.grant.Tunnel, "link", l.id, "no_relay", now)
	}
}

// verifyAccess checks a proof of password against this session's exporter
// (§9.1).
func (s *serverSession) verifyAccess(password, proof []byte) bool {
	ekm, err := s.zc.ExportKeyingMaterial(wire.AccessExportLabel, nil, wire.AccessExportLen)
	if err != nil {
		log.Debug("no session exporter for an access proof", "client", s.t.client, "err", err)
		return false
	}
	return hmac.Equal(wire.AccessProof(password, ekm), proof)
}

// holdExitLink is a server-exit LINK: the visitor's stream is the anchor.
func (s *serverSession) holdExitLink(lc *linkConn, l *peerLink) {
	l.anchor = lc
	if !s.a.newLink(l) {
		failLink(lc, true, wire.CodeInternal)
		return
	}
	defer s.a.dropLink(l)
	r := wire.LinkReply{LinkID: l.id, ServerExit: true}
	if _, err := lc.Write(wire.AppendReply(nil, wire.CodeOK, "", wire.AppendLinkReply(&r))); err != nil {
		l.end(wire.CodeInternal)
		return
	}
	_, _ = io.Copy(io.Discard, lc)
	l.end(wire.CodeOK)
}

// openProviderLink opens the LINK to the provider and registers the link.
func (a *Acceptor) openProviderLink(ctx context.Context, l *peerLink, mode uint64) (uint64, *wire.LinkReply) {
	prov := a.providerOf(l.grant.Provider)
	if prov == nil {
		return wire.CodeTunnelOffline, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return wire.CodeInternal, nil
	}
	if mode == 0 {
		mode = wire.ConnAuto
	}
	body, err := wire.AppendLinkBody(linkBodyOf(key, mode, &l.grant.Policy))
	if err != nil {
		return wire.CodeInternal, nil
	}
	l.provider = prov
	if !a.newLink(l) {
		return wire.CodeInternal, nil
	}
	a.trackUsage(l)
	info, err := wire.AppendPathOpen(&wire.PathOpen{Op: wire.OpLink, LinkID: l.id})
	if err != nil {
		a.dropLink(l)
		return wire.CodeInternal, nil
	}
	octx, cancel := context.WithTimeout(ctx, linkOpenTimeout)
	defer cancel()
	st, err := prov.zc.OpenStreamSync(octx)
	if err != nil {
		a.dropLink(l)
		return wire.CodeTunnelOffline, nil
	}
	lc := prov.conn(st)
	if !l.setAnchor(lc) {
		lc.abort(wire.CodeNotAllowed)
		a.dropLink(l)
		return wire.CodeNotAllowed, nil
	}
	if err := st.SetPreamble(info); err != nil {
		lc.abort(wire.CodeInternal)
		a.dropLink(l)
		return wire.CodeInternal, nil
	}
	if _, err := lc.Write(body); err != nil {
		lc.abort(wire.CodeInternal)
		a.dropLink(l)
		return wire.CodeTunnelOffline, nil
	}
	if err := readLinkReply(octx, lc); err != nil {
		lc.abort(wire.CodeInternal)
		a.dropLink(l)
		return linkFailCode(err), nil
	}
	a.wg.Go(func() {
		_, _ = io.Copy(io.Discard, lc)
		l.end(wire.CodeOK)
		a.dropLink(l)
	})
	return wire.CodeOK, &wire.LinkReply{LinkID: l.id, LinkKey: key, ConnMode: mode}
}

func linkBodyOf(key []byte, mode uint64, p *coreapi.LinkPolicy) *wire.LinkBody {
	b := &wire.LinkBody{LinkKey: key, ConnMode: mode, NameDest: p.NameDest, Scope: p.Scope}
	switch p.TargetType {
	case coreapi.NetworkTCP:
		b.TargetType = wire.TargetTCP
	case coreapi.NetworkUDP:
		b.TargetType = wire.TargetUDP
	}
	for _, t := range p.Targets {
		if a, err := wire.ParseHostPort(t); err == nil {
			b.Targets = append(b.Targets, a)
		}
	}
	return b
}

// RelayDisabledReason is the reason of a LINK refused because it asked for
// relay only and the tunnel does not allow relaying.
const RelayDisabledReason = "relay disabled for this tunnel"

// relayAllowed reports whether the server may relay g's link now.
func relayAllowed(g *coreapi.LinkGrant) bool {
	return g.RelayAllowed == nil || g.RelayAllowed()
}

// checkLink is the check of every PIPE, PUNCH and server-exit stream (§4.6).
func (s *serverSession) checkLink(id uint64) *peerLink {
	l := s.a.linkOf(id)
	if l == nil || l.visitor != s.ClientID() || l.grant.Valid != nil && !l.grant.Valid() {
		return nil
	}
	return l
}

// servePath connects a visitor's PIPE or PUNCH to the provider (§9.2, §9.4).
func (s *serverSession) servePath(ctx context.Context, lc *linkConn, op wire.Op, fields []byte) {
	o, err := wire.ParsePathOpen(op, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	h := s.a.hooks.Load()
	l := s.checkLink(o.LinkID)
	if l == nil || l.provider == nil || h == nil || op == wire.OpPunch && h.Coordinate == nil {
		lc.abort(wire.CodeNotAllowed)
		return
	}
	if op == wire.OpPipe && !relayAllowed(l.grant) {
		log.Info("p2p relay refused: the tunnel does not allow relaying", "tunnel", l.grant.Tunnel, "client", s.ClientID(), "link", l.id)
		lc.abort(wire.CodeNotAllowed)
		return
	}
	pctx := ctx
	if op == wire.OpPipe && l.grant.Admit != nil {
		var cancel context.CancelFunc
		pctx, cancel = context.WithCancel(ctx)
		defer cancel()
		release, err := l.grant.Admit(cancel)
		if err != nil {
			lc.abort(wire.CodeNotAllowed)
			return
		}
		defer release()
	}
	p := &linkPath{s: s, ends: []*linkConn{lc}, abort: wire.CodeNotAllowed}
	if !l.acquire(op, p) {
		// A slot still held is no refusal of the link: the visitor retries.
		lc.abort(wire.CodeInternal)
		return
	}
	defer l.release(op, p)
	octx, cancel := context.WithTimeout(ctx, linkOpenTimeout)
	st, err := l.provider.zc.OpenStreamSync(octx)
	cancel()
	if err != nil {
		lc.abort(wire.CodeTunnelOffline)
		return
	}
	end := l.provider.conn(st)
	if !p.add(end) {
		return
	}
	info, _ := wire.AppendPathOpen(&wire.PathOpen{Op: op, LinkID: l.id, Background: o.Background})
	if op == wire.OpPipe && l.provider.zc.ConnectionState().PeerIsZmux {
		_ = st.SetPipeCarrier()
	}
	if err := st.SetPreamble(info); err != nil || st.Flush() != nil {
		end.abort(wire.CodeInternal)
		lc.abort(wire.CodeInternal)
		return
	}
	if op == wire.OpPipe {
		var opts zmux.SpliceOptions
		if l.grant.Limiters != nil {
			// An expiry leaves nothing to rejoin: end the link now instead
			// of letting the nested session wait out its resume. A spent
			// traffic quota ends only the relay: the direct path is counted,
			// never limited, and further PIPEs are refused.
			stopped := func() {
				switch {
				case l.grant.Valid != nil && !l.grant.Valid():
					l.end(wire.CodeNotAllowed)
					s.a.dropLink(l)
				case !relayAllowed(l.grant):
					log.Info("p2p relay stopped: traffic quota reached", "tunnel", l.grant.Tunnel, "client", s.ClientID(), "link", l.id)
					end.abort(wire.CodeNotAllowed)
					lc.abort(wire.CodeNotAllowed)
					l.relayChanged()
				}
			}
			up, down := l.grant.Limiters()
			opts.AtoB, opts.BtoA = quotaWatch{up, stopped}, quotaWatch{down, stopped}
		}
		_, _ = zmux.Splice(pctx, lc.Stream, end.Stream, &opts)
		return
	}
	h.Coordinate(ctx, coreapi.PunchPair{Visitor: lc, Provider: end, VisitorID: l.visitor, ProviderID: l.grant.Provider,
		Tunnel: l.grant.Tunnel, Background: o.Background, ServerAddr: o.ServerAddr})
}

// quotaWatch reports a limiter that ended its direction.
type quotaWatch struct {
	coreapi.Limiter
	stopped func()
}

func (q quotaWatch) Ready(ctx context.Context, minBytes, maxBytes int) (int, error) {
	n, err := q.Limiter.Ready(ctx, minBytes, maxBytes)
	if err != nil && ctx.Err() == nil {
		q.stopped()
	}
	return n, err
}

// serveExitStream is a TCP or UDP stream of a server-exit LINK (§4.6).
func (s *serverSession) serveExitStream(ctx context.Context, lc *linkConn, op wire.Op, fields []byte) {
	o, err := wire.ParseStreamOpen(op, fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	if o.ProviderTarget && o.LinkID == 0 || o.Op == wire.OpTCP && o.Target == nil && !o.ProviderTarget {
		// provider_target needs a server-exit LINK, whose policy picks the
		// target (§9.1); a service has no server exit yet.
		failLink(lc, o.WantReply, wire.CodeMalformed)
		return
	}
	l := s.checkLink(o.LinkID)
	if o.LinkID == 0 || l == nil || !l.grant.ServerExit || l.grant.ServeExit == nil {
		failLink(lc, o.WantReply, wire.CodeNotAllowed)
		return
	}
	if o.Compress != 0 && !localCompress(o.Compress) {
		failLink(lc, o.WantReply, wire.CodeUnsupported)
		return
	}
	if !l.addExit(lc) {
		failLink(lc, o.WantReply, wire.CodeNotAllowed)
		return
	}
	defer l.dropExit(lc)
	c, err := wrapLink(lc, o, s.zc, s.a.limits)
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	var acked atomic.Bool
	ack := func() error {
		if !ackLink(lc, &o) {
			return linkError(wire.CodeInternal)
		}
		acked.Store(true)
		return nil
	}
	network, target := streamNetwork(&o)
	// The reply comes after the target is reached (§4.4); until then closing
	// the conn must not end the stream the refusal goes on.
	if err := l.grant.ServeExit(ctx, &exitConn{Conn: c, acked: &acked}, network, target, ack); err != nil && !acked.Load() {
		failLink(lc, o.WantReply, exitFailCode(err))
	}
}

// exitFailCode is the refusal of a server-exit stream.
func exitFailCode(err error) uint64 {
	var le *coreapi.LinkError
	if errors.As(err, &le) {
		return le.Code
	}
	return dialError(err)
}

// exitConn is what the exit serves: a Close before the ack leaves the stream
// to its refusal. It keeps the zero-copy and half-close of what it wraps.
type exitConn struct {
	net.Conn
	acked *atomic.Bool
}

func (c *exitConn) Close() error {
	if !c.acked.Load() {
		return nil
	}
	return c.Conn.Close()
}

func (c *exitConn) CloseWrite() error {
	if w, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return w.CloseWrite()
	}
	return c.Close()
}

func (c *exitConn) ReadFrom(r io.Reader) (int64, error) {
	if w, ok := c.Conn.(io.ReaderFrom); ok {
		return w.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{c.Conn}, r)
}

func (c *exitConn) WriteTo(w io.Writer) (int64, error) {
	if r, ok := c.Conn.(io.WriterTo); ok {
		return r.WriteTo(w)
	}
	return io.Copy(w, struct{ io.Reader }{c.Conn})
}

// streamNetwork is the network and target of a stream, as coreapi names them.
func streamNetwork(o *wire.StreamOpen) (string, string) {
	switch {
	case o.Op == wire.OpTCP && o.Target == nil:
		return coreapi.NetworkTCP, "" // provider_target: the grant's policy picks
	case o.Op == wire.OpTCP:
		return coreapi.NetworkTCP, o.Target.String()
	case o.Target != nil:
		return coreapi.NetworkUDP, o.Target.String()
	case o.ProviderTarget:
		return coreapi.NetworkUDP, ""
	}
	return coreapi.NetworkUDP5, ""
}
