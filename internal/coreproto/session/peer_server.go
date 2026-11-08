package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"sync"
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
	paths map[wire.Op]*linkPath // at most one PIPE and one PUNCH (§4.6)
}

// linkPath is the visitor's end of a PIPE or PUNCH and who opened it.
type linkPath struct {
	s     *serverSession
	ends  []*linkConn
	abort uint64
}

func (p *linkPath) stop() {
	for _, c := range p.ends {
		c.abort(p.abort)
	}
}

// acquire makes op's slot p's. A path of an earlier session of the visitor
// gives way; one of the same session keeps the slot.
func (l *peerLink) acquire(op wire.Op, p *linkPath) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return false
	}
	old := l.paths[op]
	if old != nil && old.s == p.s {
		return false
	}
	l.paths[op] = p
	if old != nil {
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

func (l *peerLink) addEnd(p *linkPath, c *linkConn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p.ends = append(p.ends, c)
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
	l.mu.Unlock()
	if anchor != nil {
		anchor.abort(code)
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
	defer a.linkMu.Unlock()
	if a.links[l.id] == l {
		delete(a.links, l.id)
	}
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
		if s.Closed() {
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
	return &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
}

func (s *serverSession) serveStream(ctx context.Context, st *zmux.Stream) {
	lc := s.conn(st)
	op, fields, err := wire.ParseOpenOp(st.Preamble())
	if err != nil {
		lc.abort(wire.CodeMalformed)
		return
	}
	switch op {
	case wire.OpLink:
		s.serveLinkOpen(ctx, lc, fields)
	case wire.OpPipe, wire.OpPunch:
		s.servePath(ctx, lc, op, fields)
	case wire.OpTCP, wire.OpUDP:
		s.serveExitStream(ctx, lc, op, fields)
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
	g, err := h.ResolveLink(s.ClientID(), coreapi.LinkResolve{Tunnel: int(o.Tunnel), Access: o.Access})
	if err != nil {
		failLink(lc, true, linkFailCode(err))
		return
	}
	l := &peerLink{visitor: s.ClientID(), grant: g, paths: map[wire.Op]*linkPath{}}
	if g.ServerExit {
		s.holdExitLink(lc, l)
		return
	}
	code, reply := s.a.openProviderLink(ctx, l, o.ConnMode)
	if code != wire.CodeOK {
		failLink(lc, true, code)
		return
	}
	_ = lc.SetWriteDeadline(time.Now().Add(linkOpenTimeout))
	_, _ = lc.Write(wire.AppendReply(nil, wire.CodeOK, "", wire.AppendLinkReply(reply)))
	_ = lc.Close() // one question, one answer: the visitor's side holds nothing
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
	p := &linkPath{s: s, ends: []*linkConn{lc}, abort: wire.CodeNotAllowed}
	if !l.acquire(op, p) {
		lc.abort(wire.CodeNotAllowed)
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
	l.addEnd(p, end)
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
		res, _ := zmux.Splice(ctx, lc.Stream, end.Stream, nil)
		if l.grant.Count != nil {
			l.grant.Count(int64(res.AtoB.Bytes), int64(res.BtoA.Bytes))
		}
		return
	}
	h.Coordinate(ctx, coreapi.PunchPair{Visitor: lc, Provider: end, VisitorID: l.visitor, ProviderID: l.grant.Provider,
		Tunnel: l.grant.Tunnel, Background: o.Background, ServerAddr: o.ServerAddr})
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
	if !ackLink(lc, &o) {
		return
	}
	c, err := wrapLink(lc, o, s.zc, s.a.limits)
	if err != nil {
		return
	}
	defer c.Close()
	network, target := streamNetwork(&o)
	l.grant.ServeExit(ctx, c, network, target)
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
