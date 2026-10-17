package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/p2pproto"
	"github.com/quic-go/quic-go"
)

const (
	// p2pOpenStreamTimeout bounds opening a stream on the direct path and
	// sending the link information over it; past it the connection falls
	// back to the relay when that is allowed.
	p2pOpenStreamTimeout = 2 * time.Second
	// p2pIdleProbeAfter is how long after the provider was last heard from
	// a new stream asks it for an acknowledgement before it is used: a
	// silently broken path (an expired NAT mapping, a changed network)
	// accepts writes until QUIC's idle timeout, so only an answer proves it.
	p2pIdleProbeAfter = 5 * time.Second
	// p2pCheckTimeout bounds one liveness check of a suspect path,
	// p2pCheckAttempts how many it gets before it is closed.
	p2pCheckTimeout  = 2 * time.Second
	p2pCheckAttempts = 2
	// p2pDialAttempts bounds the QUIC handshake attempts within the
	// session's transport timeout, p2pDialRetryDelay separating them.
	p2pDialAttempts   = 3
	p2pDialRetryDelay = 200 * time.Millisecond
	// p2pDefaultTransportTimeout applies when the session carries none.
	p2pDefaultTransportTimeout = 10 * time.Second
	// p2pDefaultIdleTimeout applies when the path has no keepalive advice.
	p2pDefaultIdleTimeout = 30 * time.Second
)

// QUIC application error codes on the punched path.
const (
	p2pCodeClosed         quic.ApplicationErrorCode = 0
	p2pCodeUnexpectedPeer quic.ApplicationErrorCode = 1
	p2pCodeLost           quic.ApplicationErrorCode = 2
	// p2pStreamAbandoned resets a stream whose link was not delivered.
	p2pStreamAbandoned quic.StreamErrorCode = 1
)

// p2pALPN is the application protocol of the QUIC connection on the punched
// path: HTTP/3's, the most common one on UDP, so that the connection does not
// stand out (the bridge's own QUIC uses quic_alpn, nps by default). Both
// sides authenticate by their pinned certificates alone.
const p2pALPN = "h3"

var errP2PTransportMode = errors.New("p2p session did not settle on a pinned QUIC transport")

// p2pTransport is a QUIC connection on a punched path, together with the
// QUIC transport reading the socket and the release of the punched socket
// (the holepunch session's Close, which also drops a port mapping). Close
// releases all three; it follows the QUIC connection's end.
type p2pTransport struct {
	qc      *quic.Conn
	tr      *quic.Transport
	release func() error

	lastHeard atomic.Int64 // unix nanoseconds of the provider's last answer
	suspect   atomic.Bool  // a connection failed on it; a check is deciding
	closeOnce sync.Once
}

func newP2PTransport(qc *quic.Conn, tr *quic.Transport, release func() error) *p2pTransport {
	t := &p2pTransport{qc: qc, tr: tr, release: release}
	t.heard() // the handshake just completed
	return t
}

// Done is closed when the QUIC connection ends, for whatever reason.
func (t *p2pTransport) Done() <-chan struct{} { return t.qc.Context().Done() }

// Alive reports whether the QUIC connection is still up.
func (t *p2pTransport) Alive() bool { return t.qc.Context().Err() == nil }

// heard records an answer of the provider.
func (t *p2pTransport) heard() { t.lastHeard.Store(time.Now().UnixNano()) }

// silence is the time since the provider last answered.
func (t *p2pTransport) silence() time.Duration {
	return time.Since(time.Unix(0, t.lastHeard.Load()))
}

// Close closes the QUIC connection with code, stops the transport and
// releases the socket. It is idempotent.
func (t *p2pTransport) Close(code quic.ApplicationErrorCode, reason string) {
	t.closeOnce.Do(func() {
		_ = t.qc.CloseWithError(code, reason)
		_ = t.tr.Close()
		if t.release != nil {
			_ = t.release()
		}
	})
}

// openLink opens a stream and sends link over it; when the provider has not
// answered for p2pIdleProbeAfter it also waits for its acknowledgement. Each
// step is bounded by p2pOpenStreamTimeout. On failure the stream is
// reset in both directions, so that the provider drops whatever part of the
// link it received, and no user data has been written to it.
func (t *p2pTransport) openLink(ctx context.Context, link conn.Link) (net.Conn, error) {
	link.Option.NeedAck = t.silence() > p2pIdleProbeAfter
	return t.open(ctx, link, p2pOpenStreamTimeout)
}

// check asks the provider for an acknowledgement on a stream of its own: a
// link without a host, which the provider acknowledges and closes.
func (t *p2pTransport) check(ctx context.Context) error {
	c, err := t.open(ctx, conn.Link{Option: conn.Options{NeedAck: true, Timeout: p2pCheckTimeout}}, p2pCheckTimeout)
	if err == nil {
		_ = c.Close()
	}
	return err
}

func (t *p2pTransport) open(ctx context.Context, link conn.Link, timeout time.Duration) (net.Conn, error) {
	octx, cancel := context.WithTimeout(ctx, timeout)
	stream, err := t.qc.OpenStreamSync(octx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	fail := func(err error) (net.Conn, error) {
		stream.CancelWrite(p2pStreamAbandoned)
		stream.CancelRead(p2pStreamAbandoned)
		return nil, err
	}
	nc := conn.NewQuicStreamConn(stream, t.qc)
	_ = stream.SetWriteDeadline(time.Now().Add(timeout))
	// The wrapper only writes to nc, whose stream fail cancels on failure.
	//noinspection GoResourceLeak
	if _, err := conn.NewConn(nc).SendInfo(&link, ""); err != nil {
		return fail(fmt.Errorf("send link: %w", err))
	}
	_ = stream.SetWriteDeadline(time.Time{})
	if link.Option.NeedAck {
		if err := conn.ReadACK(nc, timeout); err != nil {
			return fail(fmt.Errorf("read ack: %w", err))
		}
		t.heard()
	}
	return nc, nil
}

// p2pQUICConfig derives the QUIC configuration from the punched path: the
// keepalive and idle timeout follow the path's keepalive advice, so that a
// dead path is noticed as early as the NAT allows, and the handshake is
// bounded by the session's transport timeout.
func p2pQUICConfig(res holepunch.Result) *quic.Config {
	idle := res.Path.Keepalive.Timeout
	if idle <= 0 {
		idle = p2pDefaultIdleTimeout
	}
	keepalive := res.Path.Keepalive.Interval
	if keepalive <= 0 || keepalive >= idle {
		keepalive = idle / 3
	}
	return &quic.Config{
		HandshakeIdleTimeout: p2pTransportTimeout(res),
		MaxIdleTimeout:       idle,
		KeepAlivePeriod:      keepalive,
		MaxIncomingStreams:   QuicConfig.MaxIncomingStreams,
	}
}

func p2pTransportTimeout(res holepunch.Result) time.Duration {
	if res.TransportTimeout > 0 {
		return res.TransportTimeout
	}
	return p2pDefaultTransportTimeout
}

// p2pLocalPin is this side's certificate pin, sent to the peer as
// TransportData.
func p2pLocalPin() string {
	cert := crypt.GetCert()
	if len(cert.Certificate) == 0 {
		return ""
	}
	return crypt.EncodePeerTransportData(cert.Certificate[0])
}

// p2pServerTLS is the provider's QUIC TLS configuration: the visitor must
// present the certificate with pin, the one the visitor sent through the
// coordinator. No session tickets: every connection authenticates afresh.
func p2pServerTLS(pin string) *tls.Config {
	return crypt.PinPeerTransportData(pin).ServerConfig(&tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{crypt.GetCert()},
		SessionTicketsDisabled: true,
		NextProtos:             []string{p2pALPN},
	})
}

// p2pClientTLS is the visitor's QUIC TLS configuration: the provider's pin,
// sent through the coordinator, replaces the chain verification.
func p2pClientTLS(pin string) *tls.Config {
	return crypt.PinPeerTransportData(pin).ClientConfig(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{crypt.GetCert()},
		ServerName:   crypt.GetFakeDomainName(),
		NextProtos:   []string{p2pALPN},
	})
}

// p2pSocket checks that sess settled on a pinned QUIC transport and returns
// the peer's pin and the punched socket with clear deadlines.
func p2pSocket(sess *holepunch.Session) (holepunch.Result, string, net.PacketConn, error) {
	res := sess.Result()
	if res.TransportMode != p2pproto.TransportMode || res.TransportData == "" {
		return res, "", nil, fmt.Errorf("%w: mode %q", errP2PTransportMode, res.TransportMode)
	}
	pc := sess.PacketConn()
	if err := pc.SetDeadline(time.Time{}); err != nil {
		return res, "", nil, err
	}
	return res, res.TransportData, pc, nil
}

// dialP2PTransport connects QUIC to the provider over the punched path, in
// at most p2pDialAttempts within the session's transport timeout. It takes
// sess over: on failure sess is closed.
func dialP2PTransport(ctx context.Context, sess *holepunch.Session) (*p2pTransport, error) {
	res, pin, pc, err := p2pSocket(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	tr := &quic.Transport{Conn: pc}
	dctx, cancel := context.WithTimeout(ctx, p2pTransportTimeout(res))
	defer cancel()
	remote := net.UDPAddrFromAddrPort(res.Path.RemoteAddr)
	tlsCfg, qcfg := p2pClientTLS(pin), p2pQUICConfig(res)
	var last error
	for attempt := 1; ; attempt++ {
		qc, err := tr.Dial(dctx, remote, tlsCfg, qcfg)
		if err == nil {
			return newP2PTransport(qc, tr, sess.Close), nil
		}
		last = err
		if attempt >= p2pDialAttempts || dctx.Err() != nil || p2pDialFinal(err) || !sleepWithContext(dctx, p2pDialRetryDelay) {
			break
		}
	}
	_ = tr.Close()
	_ = sess.Close()
	return nil, fmt.Errorf("quic dial %v: %w", res.Path.RemoteAddr, last)
}

// acceptP2PTransport waits, within the session's transport timeout, for the
// visitor's QUIC connection from the punched address with the visitor's pin.
// It takes sess over: on failure sess is closed.
func acceptP2PTransport(ctx context.Context, sess *holepunch.Session) (*p2pTransport, error) {
	res, pin, pc, err := p2pSocket(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	tr := &quic.Transport{Conn: pc}
	ln, err := tr.Listen(p2pServerTLS(pin), p2pQUICConfig(res))
	if err != nil {
		_ = tr.Close()
		_ = sess.Close()
		return nil, fmt.Errorf("quic listen: %w", err)
	}
	defer func() { _ = ln.Close() }()
	actx, cancel := context.WithTimeout(ctx, p2pTransportTimeout(res))
	defer cancel()
	for {
		qc, err := ln.Accept(actx)
		if err != nil {
			_ = tr.Close()
			_ = sess.Close()
			return nil, fmt.Errorf("quic accept: %w", err)
		}
		if sameAddrPort(qc.RemoteAddr(), res.Path.RemoteAddr) {
			return newP2PTransport(qc, tr, sess.Close), nil
		}
		_ = qc.CloseWithError(p2pCodeUnexpectedPeer, "unexpected peer")
	}
}

// p2pDialFinal reports a dial error another attempt cannot cure: a closed
// socket or transport, or a TLS failure such as a pin mismatch.
func p2pDialFinal(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	var te *quic.TransportError
	return errors.As(err, &te) && te.ErrorCode.IsCryptoError()
}

// sameAddrPort compares a socket address with the punched peer address.
func sameAddrPort(a net.Addr, want netip.AddrPort) bool {
	ua, ok := a.(*net.UDPAddr)
	if !ok {
		return false
	}
	got := ua.AddrPort()
	return got.Addr().Unmap() == want.Addr().Unmap() && got.Port() == want.Port()
}
