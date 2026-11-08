package session

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/logevent"
	"github.com/zmuxio/zmux-go"
)

// exitDialTimeout is how long the exit tries a target (§4.4).
const exitDialTimeout = 5 * time.Second

// serveStreams answers what the server opens: TCP and UDP links, and refusals
// for everything else.
func (s *clientSession) serveStreams(ctx context.Context) {
	for {
		st, err := s.zc.AcceptStream(ctx)
		if err != nil {
			return
		}
		s.wg.Go(func() { s.serveStream(ctx, st) })
	}
}

func (s *clientSession) serveStream(ctx context.Context, st *zmux.Stream) {
	lc := &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
	op, fields, err := wire.ParseOpenOp(st.Preamble())
	if err != nil {
		lc.abort(wire.CodeMalformed)
		return
	}
	if op != wire.OpTCP && op != wire.OpUDP {
		lc.abort(rejectCode(st.Preamble(), wire.Op.FromServer))
		return
	}
	o, err := wire.ParseStreamOpen(op, fields)
	if err != nil {
		// No reply head yet: the shape of the request is unknown (§4.2).
		lc.abort(wire.ErrorCode(err))
		return
	}
	code := s.checkOpen(&o)
	if code != wire.CodeOK {
		failLink(lc, o.WantReply, code)
		return
	}
	s.serveLink(ctx, lc, &o)
}

// checkOpen is what the exit decides before it dials (§7.2, §8).
func (s *clientSession) checkOpen(o *wire.StreamOpen) uint64 {
	if o.Compress != 0 && !localCompress(o.Compress) {
		return wire.CodeUnsupported
	}
	if len(o.Service) > 0 {
		return wire.CodeServiceUnavailable
	}
	if s.d.cfg.Exit == nil {
		return wire.CodeTunnelOffline
	}
	return wire.CodeOK
}

func localCompress(alg uint64) bool {
	for _, a := range compressAlgs {
		if a == alg {
			return true
		}
	}
	return false
}

// failLink refuses a stream with code: a reply head first when one was asked
// for, then a reset (§4.2).
func failLink(lc *linkConn, wantReply bool, code uint64) {
	if wantReply {
		_ = lc.SetWriteDeadline(time.Now().Add(exitDialTimeout))
		if _, err := lc.Write(wire.AppendReply(nil, code, "", nil)); err == nil {
			lc.SetReliableBoundary()
		}
	}
	lc.abort(code)
}

// dialError maps a dial failure to its code (§10).
func dialError(err error) uint64 {
	switch logevent.NetReason(err) {
	case "refused":
		return wire.CodeConnectionRefused
	case "timeout":
		return wire.CodeConnectTimeout
	case "dns":
		return wire.CodeDNSFailure
	case "permission":
		return wire.CodeNotAllowed
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "network is unreachable"):
		return wire.CodeNetworkUnreachable
	case strings.Contains(s, "unreachable"), strings.Contains(s, "no route to host"):
		return wire.CodeHostUnreachable
	}
	return wire.CodeGeneralFailure
}

// serveLink dials the target of a stream, replies when asked, and relays.
func (s *clientSession) serveLink(ctx context.Context, lc *linkConn, o *wire.StreamOpen) {
	exit := s.d.cfg.Exit
	network := coreapi.NetworkTCP
	if o.Op == wire.OpUDP {
		network = coreapi.NetworkUDP
	}
	if o.Target == nil {
		// Addressed UDP: no dial, the exit relays each datagram (§4.5).
		if !ackLink(lc, o) {
			return
		}
		c, _ := wrapLink(lc, *o, s.zc, s.d.limits)
		exit.ServeUDP5(ctx, c)
		_ = c.Close()
		return
	}
	dctx, cancel := context.WithTimeout(ctx, exitDialTimeout)
	target, err := exit.Dial(dctx, network, o.Target.String())
	cancel()
	if err != nil {
		log.Debug("dial target failed", "target", o.Target.String(), "err", err)
		failLink(lc, o.WantReply, dialError(err))
		return
	}
	if !ackLink(lc, o) {
		_ = target.Close()
		return
	}
	c, err := wrapLink(lc, *o, s.zc, s.d.limits)
	if err != nil {
		_ = target.Close()
		return
	}
	var end net.Conn = c
	if o.Op == wire.OpUDP {
		end = netx.NewFramedConn(c, netx.WithFramedDatagrams())
	}
	_, _, _ = netx.Relay(ctx, end, target, netx.WithRelayHalfClose())
}

func ackLink(lc *linkConn, o *wire.StreamOpen) bool {
	if !o.WantReply {
		return true
	}
	_ = lc.SetWriteDeadline(time.Now().Add(exitDialTimeout))
	_, err := lc.Write(wire.AppendReply(nil, wire.CodeOK, "", nil))
	_ = lc.SetWriteDeadline(time.Time{})
	if err != nil {
		lc.abort(wire.CodeInternal)
		return false
	}
	return true
}
