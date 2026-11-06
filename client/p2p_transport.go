package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/djylb/holepunch"
	"github.com/djylb/holepunch/quicpath"
	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
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
)

// p2pStreamAbandoned resets a stream whose link was not delivered.
const p2pStreamAbandoned quic.StreamErrorCode = 1

// p2pALPN is the application protocol of the QUIC connection on the punched
// path: HTTP/3's, the most common one on UDP, so that the connection does not
// stand out (the bridge's own QUIC uses quic_alpn, nps by default). Both
// sides authenticate by their pinned certificates alone.
const p2pALPN = "h3"

var errP2PTransportMode = errors.New("p2p session did not settle on a pinned QUIC transport")

// p2pPipe is a QUIC connection on a punched path: a *quicpath.Conn, which
// also owns the holepunch session.
type p2pPipe interface {
	QUIC() *quic.Conn
	Done() <-chan struct{}
	Err() error
	Close() error
}

// p2pTransport is the visitor's pipe to the provider together with the
// liveness the visitor tracks on it.
type p2pTransport struct {
	p2pPipe

	lastHeard atomic.Int64 // unix nanoseconds of the provider's last answer
	suspect   atomic.Bool  // a connection failed on it; a check is deciding
}

func newP2PTransport(c p2pPipe) *p2pTransport {
	t := &p2pTransport{p2pPipe: c}
	t.heard() // the handshake just completed
	return t
}

// Alive reports whether the QUIC connection is still up.
func (t *p2pTransport) Alive() bool { return t.Err() == nil }

// heard records an answer of the provider.
func (t *p2pTransport) heard() { t.lastHeard.Store(time.Now().UnixNano()) }

// silence is the time since the provider last answered.
func (t *p2pTransport) silence() time.Duration {
	return time.Since(time.Unix(0, t.lastHeard.Load()))
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
	stream, err := t.QUIC().OpenStreamSync(octx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	fail := func(err error) (net.Conn, error) {
		stream.CancelWrite(p2pStreamAbandoned)
		stream.CancelRead(p2pStreamAbandoned)
		return nil, err
	}
	nc := conn.NewQuicStreamConn(stream, t.QUIC())
	_ = stream.SetWriteDeadline(time.Now().Add(timeout))
	// The wrapper only writes to nc, whose stream fail cancels on failure.
	//noinspection GoResourceLeak
	if err := legacy.WriteLink(nc, &link); err != nil {
		return fail(fmt.Errorf("send link: %w", err))
	}
	_ = stream.SetWriteDeadline(time.Time{})
	if link.Option.NeedAck {
		if err := legacy.ReadAck(nc, timeout); err != nil {
			return fail(fmt.Errorf("read ack: %w", err))
		}
		t.heard()
	}
	return nc, nil
}

// p2pQUICConfig is the QUIC configuration on a punched path; quicpath
// derives the keepalive, idle and handshake timeouts from the session.
func p2pQUICConfig() *quic.Config {
	return &quic.Config{MaxIncomingStreams: QuicConfig.MaxIncomingStreams}
}

// p2pLocalPin is this side's certificate fingerprint, sent to the peer as
// TransportData.
func p2pLocalPin() []byte {
	return crypt.GetCertFingerprint(crypt.GetCert())
}

// p2pServerTLS is the provider's QUIC TLS configuration: the visitor must
// present the certificate with pin, the fingerprint the visitor sent through
// the coordinator. No session tickets: every connection authenticates
// afresh.
func p2pServerTLS(pin []byte) *tls.Config {
	return crypt.PinFingerprint(pin).ServerConfig(&tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{crypt.GetCert()},
		SessionTicketsDisabled: true,
		NextProtos:             []string{p2pALPN},
	})
}

// p2pClientTLS is the visitor's QUIC TLS configuration: the provider's
// fingerprint, sent through the coordinator, replaces the chain
// verification.
func p2pClientTLS(pin []byte) *tls.Config {
	return crypt.PinFingerprint(pin).ClientConfig(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{crypt.GetCert()},
		ServerName:   crypt.GetFakeDomainName(),
		NextProtos:   []string{p2pALPN},
	})
}

// p2pPeerPin checks that sess settled on a pinned QUIC transport and
// returns the peer's pin.
func p2pPeerPin(sess *holepunch.Session) ([]byte, error) {
	res := sess.Result()
	if res.TransportMode != legacy.P2PTransportMode || len(res.PeerTransportData) == 0 {
		return nil, fmt.Errorf("%w: mode %q", errP2PTransportMode, res.TransportMode)
	}
	return res.PeerTransportData, nil
}

// dialP2PTransport connects QUIC to the provider over the punched path. It
// takes sess over: on failure sess is closed.
func dialP2PTransport(ctx context.Context, sess *holepunch.Session) (*p2pTransport, error) {
	pin, err := p2pPeerPin(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	c, err := quicpath.Dial(ctx, sess, p2pClientTLS(pin), p2pQUICConfig())
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("quic dial %v: %w", sess.Result().Path.RemoteAddr, err)
	}
	return newP2PTransport(c), nil
}

// acceptP2PTransport waits for the visitor's QUIC connection from the
// punched address with the visitor's pin. It takes sess over: on failure
// sess is closed.
func acceptP2PTransport(ctx context.Context, sess *holepunch.Session) (*quicpath.Conn, error) {
	pin, err := p2pPeerPin(sess)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	c, err := quicpath.Accept(ctx, sess, p2pServerTLS(pin), p2pQUICConfig())
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("quic accept: %w", err)
	}
	return c, nil
}
