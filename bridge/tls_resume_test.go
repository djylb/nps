package bridge

import (
	"crypto/rand"
	"crypto/tls"
	"net"
	"testing"
)

// Carrier TLS configs are built per connection but share the bridge's ticket
// key, so a client resumes its session on the next connection.
func TestCarrierTLSConfigsResume(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	s := &Bridge{cert: selfSigned(t, "bridge.test"), ticketKey: func() [32]byte { return key }}
	cache := tls.NewLRUClientSessionCache(0)
	handshake := func() bool {
		t.Helper()
		a, b := net.Pipe()
		defer func() { _ = a.Close(); _ = b.Close() }()
		srv := tls.Server(a, s.tlsConfig())
		done := make(chan error, 1)
		go func() {
			err := srv.Handshake()
			if err == nil {
				// TLS 1.3 sends its ticket after the handshake: let the
				// client read it.
				_, err = srv.Write([]byte{1})
			}
			done <- err
		}()
		cli := tls.Client(b, &tls.Config{InsecureSkipVerify: true, ClientSessionCache: cache, ServerName: "bridge.test"}) //nolint:gosec // test peer
		if err := cli.Handshake(); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		return srv.ConnectionState().DidResume
	}
	if handshake() {
		t.Fatal("first handshake resumed")
	}
	if !handshake() {
		t.Fatal("second handshake did not resume across carrier configs")
	}
}
