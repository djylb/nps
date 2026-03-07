package conn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

var (
	testCertOnce sync.Once
	testCert     tls.Certificate
)

type deadlineSpyConn struct {
	net.Conn
	mu            sync.Mutex
	lastDeadline  time.Time
	deadlineCalls int
}

func (d *deadlineSpyConn) SetDeadline(t time.Time) error {
	d.mu.Lock()
	d.lastDeadline = t
	d.deadlineCalls++
	d.mu.Unlock()
	return d.Conn.SetDeadline(t)
}

func (d *deadlineSpyConn) snapshot() (time.Time, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastDeadline, d.deadlineCalls
}

type closeSpyConn struct {
	net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *closeSpyConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *closeSpyConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func TestTimeoutConnReadWriteSetsDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	spy := &deadlineSpyConn{Conn: client}
	idle := 300 * time.Millisecond
	conn := NewTimeoutConn(spy, idle)

	start := time.Now()
	go func() {
		_, _ = server.Write([]byte("hi"))
	}()

	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got := string(buf[:n]); got != "hi" {
		t.Fatalf("read payload mismatch: got %q", got)
	}

	firstDeadline, firstCalls := spy.snapshot()
	if firstCalls == 0 {
		t.Fatal("expected SetDeadline to be called on read")
	}
	if firstDeadline.Before(start.Add(idle - 120*time.Millisecond)) {
		t.Fatalf("deadline not set close to now+idle: got %v", firstDeadline)
	}

	serverRead := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	go func() {
		tmp := make([]byte, 2)
		_, readErr := io.ReadFull(server, tmp)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		serverRead <- tmp
	}()

	if _, err = conn.Write([]byte("ok")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	select {
	case readErr := <-serverErr:
		t.Fatalf("server read failed: %v", readErr)
	case got := <-serverRead:
		if string(got) != "ok" {
			t.Fatalf("write payload mismatch: got %q", string(got))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server read timed out")
	}

	secondDeadline, secondCalls := spy.snapshot()
	if secondCalls < 2 {
		t.Fatalf("expected SetDeadline called for read and write, got %d", secondCalls)
	}
	if !secondDeadline.After(firstDeadline) {
		t.Fatalf("expected write deadline to refresh, first=%v second=%v", firstDeadline, secondDeadline)
	}
}

func TestNewTimeoutTLSConnSuccess(t *testing.T) {
	cert := testSelfSignedCert(t)

	clientRaw, serverRaw := net.Pipe()
	defer func() { _ = serverRaw.Close() }()
	serverErr := make(chan error, 1)
	go func() {
		tlsServer := tls.Server(serverRaw, &tls.Config{Certificates: []tls.Certificate{cert}})
		defer func() { _ = tlsServer.Close() }()
		if err := tlsServer.Handshake(); err != nil {
			serverErr <- err
			return
		}

		buf := make([]byte, 4)
		if _, err := io.ReadFull(tlsServer, buf); err != nil {
			serverErr <- err
			return
		}
		_, err := tlsServer.Write(buf)
		serverErr <- err
	}()

	conn, err := NewTimeoutTLSConn(clientRaw, &tls.Config{InsecureSkipVerify: true}, 500*time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatalf("NewTimeoutTLSConn failed: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, ok := conn.(*TimeoutConn); !ok {
		t.Fatalf("expected *TimeoutConn, got %T", conn)
	}

	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatalf("tls write failed: %v", err)
	}
	buf := make([]byte, 4)
	if _, err = io.ReadFull(conn, buf); err != nil {
		t.Fatalf("tls read failed: %v", err)
	}
	if got := string(buf); got != "ping" {
		t.Fatalf("echo mismatch: got %q", got)
	}

	if err = <-serverErr; err != nil {
		t.Fatalf("server side failed: %v", err)
	}
}

func TestNewTimeoutTLSConnHandshakeFailureClosesRaw(t *testing.T) {
	cert := testSelfSignedCert(t)

	clientRaw, serverRaw := net.Pipe()
	spy := &closeSpyConn{Conn: clientRaw}
	defer func() { _ = serverRaw.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tlsServer := tls.Server(serverRaw, &tls.Config{Certificates: []tls.Certificate{cert}})
		defer func() { _ = tlsServer.Close() }()
		_ = tlsServer.Handshake()
	}()

	conn, err := NewTimeoutTLSConn(spy, &tls.Config{ServerName: "example.com"}, 200*time.Millisecond, 2*time.Second)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected handshake to fail")
	}
	if conn != nil {
		t.Fatalf("expected nil conn on handshake failure, got %T", conn)
	}
	if !spy.isClosed() {
		t.Fatal("expected raw conn to be closed on handshake failure")
	}
	<-done
}

func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key failed: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		DNSNames: []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert failed: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key failed: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load key pair failed: %v", err)
	}
	return cert
}

func testSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	testCertOnce.Do(func() {
		testCert = generateSelfSignedCert(t)
	})
	return testCert
}
