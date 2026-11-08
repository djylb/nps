package session

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

const streamCancelled = zmux.StreamCancelled

// replyLimit bounds a reply head of a TCP or UDP stream.
const replyLimit = 4 << 10

// linkConn is the net.Conn of a stream. Close ends both directions, CloseWrite
// sends FIN alone, and the methods of the stream it embeds (ReadFrom, WriteTo)
// keep relays on its zero-copy path.
type linkConn struct {
	*zmux.Stream
	local, remote net.Addr
}

func (c *linkConn) Close() error {
	err := c.Stream.Close()
	c.CancelRead(zmux.StreamNoError)
	return err
}

func (c *linkConn) CloseWrite() error    { return c.Stream.Close() }
func (c *linkConn) LocalAddr() net.Addr  { return addrOrZero(c.local) }
func (c *linkConn) RemoteAddr() net.Addr { return addrOrZero(c.remote) }

// addrOrZero keeps the net.Conn contract where the session has no address:
// zmux reports none for a wrapped carrier.
func addrOrZero(a net.Addr) net.Addr {
	if a == nil {
		return &net.TCPAddr{}
	}
	return a
}

func (c *linkConn) abort(code uint64) {
	e := zmux.StreamErrorCode(code)
	c.CancelWrite(e)
	c.CancelRead(e)
}

// linkReasons are the words of LinkError (§10), which logevent.NetReason
// gives to the log and the visitor mapping reads.
var linkReasons = map[uint64]string{
	wire.CodeConnectionRefused:  "refused",
	wire.CodeConnectTimeout:     "timeout",
	wire.CodeDNSFailure:         "dns",
	wire.CodeNetworkUnreachable: "unreachable",
	wire.CodeHostUnreachable:    "unreachable",
	wire.CodeNotAllowed:         "permission",
	wire.CodeAddrTypeUnsupp:     "other",
	wire.CodeUnsupported:        "other",
	wire.CodeMalformed:          "other",
	wire.CodeTunnelOffline:      "client_offline",
	wire.CodeCompressError:      "other",
}

func linkError(code uint64) *coreapi.LinkError {
	r, ok := linkReasons[code]
	if !ok {
		r = "other"
	}
	return &coreapi.LinkError{Code: code, Reason: r}
}

// openLink opens the TCP or UDP stream of r on zc, on the side that is the
// entry (§4.4, §4.5). peerAlgs is what the other side decompresses.
func openLink(ctx context.Context, zc *zmux.Conn, r coreapi.LinkRequest, peerAlgs []uint64, limits CompressLimits) (net.Conn, error) {
	o := wire.StreamOpen{Op: wire.OpTCP, WantReply: r.WantReply, LinkID: r.LinkID, Service: r.Service}
	if r.Network == coreapi.NetworkUDP {
		o.Op = wire.OpUDP
	} else {
		o.Compress = chooseCompress(r.Compress, peerAlgs)
	}
	if r.Target != "" {
		a, err := wire.ParseHostPort(r.Target)
		if err != nil {
			return nil, linkError(wire.CodeMalformed)
		}
		o.Target = &a
	} else if o.Op == wire.OpTCP && !r.ProviderTarget && len(r.Service) == 0 {
		return nil, linkError(wire.CodeMalformed)
	}
	o.ProviderTarget = r.ProviderTarget
	if a, err := wire.ParseHostPort(r.Source); err == nil {
		o.Source = &a
	}
	info, err := wire.AppendStreamOpen(&o)
	if err != nil {
		return nil, linkError(wire.CodeMalformed)
	}
	st, err := zc.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	lc := &linkConn{Stream: st, local: zc.LocalAddr(), remote: zc.RemoteAddr()}
	if err := st.SetPreamble(info); err != nil {
		lc.abort(wire.CodeInternal)
		return nil, err
	}
	// A server that speaks first gets its stream without entry data (§4.1).
	if err := st.Flush(); err != nil {
		lc.abort(wire.CodeInternal)
		return nil, err
	}
	if r.WantReply {
		if err := readLinkReply(ctx, lc); err != nil {
			lc.abort(wire.CodeInternal)
			return nil, err
		}
	}
	return wrapLink(lc, o, zc, limits)
}

// readLinkReply waits for the head of a want_reply stream (§4.2), within ctx.
func readLinkReply(ctx context.Context, lc *linkConn) error {
	if dl, ok := ctx.Deadline(); ok {
		_ = lc.SetReadDeadline(dl)
		defer func() { _ = lc.SetReadDeadline(time.Time{}) }()
	}
	rep, err := wire.ReadReply(lc, replyLimit)
	if err != nil {
		return replyReadError(err)
	}
	if rep.Code != wire.CodeOK {
		return linkError(rep.Code)
	}
	return nil
}

func replyReadError(err error) error {
	var se *zmux.StreamError
	switch {
	case errors.As(err, &se):
		return linkError(uint64(se.ErrorCode))
	case errors.Is(err, wire.ErrMalformed):
		return linkError(wire.CodeMalformed)
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return linkError(wire.CodeConnectTimeout)
	}
	return err
}

// wrapLink gives the application its view of a stream: compressed bytes
// decoded, or UDP packets as the frames the tunnel code reads.
func wrapLink(lc *linkConn, o wire.StreamOpen, zc *zmux.Conn, limits CompressLimits) (net.Conn, error) {
	if o.Op == wire.OpUDP {
		return newUDPConn(lc, o.Target == nil && !o.ProviderTarget, zc), nil
	}
	if o.Compress != 0 {
		c, err := newCompressConn(lc, o.Compress, limits)
		if err != nil {
			lc.abort(wire.CodeInternal)
			return nil, err
		}
		return c, nil
	}
	return lc, nil
}

// chooseCompress is the first algorithm of want the peer can decompress, 0
// for none (§7.2).
func chooseCompress(want []string, peer []uint64) uint64 {
	for _, name := range want {
		id, ok := compressNames[name]
		if !ok {
			continue
		}
		for _, a := range peer {
			if a == id {
				return id
			}
		}
	}
	if len(want) > 0 {
		log.Debug("compression not supported by the peer", "detail", want[0])
	}
	return 0
}

// OpenLink implements coreapi.Session.
func (s *serverSession) OpenLink(ctx context.Context, r coreapi.LinkRequest) (net.Conn, error) {
	return openLink(ctx, s.zc, r, s.t.hello.CompressAlgs, s.a.limits)
}

// Ping implements coreapi.Session.
func (s *serverSession) Ping(ctx context.Context) (time.Duration, error) { return s.zc.Ping(ctx) }

// Closed implements coreapi.Session.
func (s *serverSession) Closed() bool { return s.zc.Context().Err() != nil }
