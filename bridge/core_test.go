package bridge

import (
	"bytes"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"testing"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/quic-go/quic-go"
)

// stubFallback offers a certificate for one name only.
type stubFallback struct {
	name string
	cert *tls.Certificate
}

func (stubFallback) ServeFallback(net.Conn, *tls.ConnectionState) bool { return false }

func (stubFallback) ServeHTTP3(*quic.Conn, func(http.Handler) http.Handler) bool { return false }

func (f stubFallback) FallbackCert(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if h.ServerName == f.name {
		return f.cert, nil
	}
	return nil, nil
}

func selfSigned(t *testing.T, host string) tls.Certificate {
	t.Helper()
	c, err := tlsconn.NewSelfSigned(tlsconn.SelfSignedOptions{Hosts: []string{host}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// served returns the certificate a client asking for sni gets from cfg.
func served(t *testing.T, cfg *tls.Config, sni string) []byte {
	t.Helper()
	c, s := net.Pipe()
	srv := tls.Server(s, cfg)
	var wg sync.WaitGroup
	wg.Go(func() { _ = srv.Handshake(); _ = srv.Close() })
	cl := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: sni})
	err := cl.Handshake()
	_ = c.Close()
	wg.Wait()
	if err != nil {
		t.Fatalf("handshake for %q: %v", sni, err)
	}
	return cl.ConnectionState().PeerCertificates[0].Raw
}

// With the new protocol on, the TLS bridge presents the certificate of the
// route the name belongs to, and its own only when none does.
func TestTLSBridgeCertificateByName(t *testing.T) {
	own, route := selfSigned(t, "bridge.test"), selfSigned(t, "site.test")
	b := newBridge(&sync.Map{}, 0)
	b.cert = own
	b.core = stubEntry{}
	b.SetFallback(stubFallback{name: "site.test", cert: &route})
	if got := served(t, b.tlsConfig(), "site.test"); !bytes.Equal(got, route.Certificate[0]) {
		t.Fatal("the route's certificate is not presented for its name")
	}
	for _, sni := range []string{"unknown.test", ""} {
		if got := served(t, b.tlsConfig(), sni); !bytes.Equal(got, own.Certificate[0]) {
			t.Fatalf("name %q: the bridge certificate is not the default", sni)
		}
	}
	b.SetFallback(nil)
	if got := served(t, b.tlsConfig(), "site.test"); !bytes.Equal(got, own.Certificate[0]) {
		t.Fatal("without a fallback the bridge certificate is not presented")
	}
}

// stubEntry is the entry of tests that do not run the new protocol.
type stubEntry struct{ coreapi.Entry }

func (stubEntry) Kick(int, bool)                                       {}
func (stubEntry) RevokeLinks(func(tunnel, visitor, provider int) bool) {}
func (stubEntry) Forwarding([]netip.Prefix, string) coreapi.Forwarding { return nil }
