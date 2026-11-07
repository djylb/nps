package httpproxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/file"
)

// newTestDb returns an empty host database of its own.
func newTestDb(t *testing.T) *file.DbUtils {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &file.DbUtils{JsonDb: file.NewJsonDb(root)}
}

// newTestHttp3Server returns an HTTP/3 server of the hosts in store.
func newTestHttp3Server(t *testing.T, store Store) *Http3Server {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	https := &HttpsServer{
		HttpServer:   &HttpServer{HttpProxy: &HttpProxy{ForceAutoSsl: true, store: store}},
		certMagicTls: &tls.Config{},
	}
	return NewHttp3Server(https, pc)
}

func TestHttp3SkipsTlsOffloadHost(t *testing.T) {
	db := newTestDb(t)
	host := &model.Host{Id: 1, Host: "offload.example", Scheme: "all", AutoSSL: true, Client: &model.Client{Id: 1}}
	if err := db.NewHost(host); err != nil {
		t.Fatal(err)
	}

	s := newTestHttp3Server(t, db)
	hello := &tls.ClientHelloInfo{ServerName: host.Host}
	if cfg, _ := s.GetConfigForClient(hello); cfg == nil {
		t.Fatalf("regular host must get an HTTP/3 TLS config")
	}
	host.TlsOffload = true
	if cfg, _ := s.GetConfigForClient(hello); cfg != nil {
		t.Fatalf("TLS offload host must not be terminated by HTTP/3")
	}
}

func TestHttp3CloseBeforeStart(t *testing.T) {
	s := newTestHttp3Server(t, newTestDb(t))
	s.httpsStatus.Store(true)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !s.httpsStatus.Load() {
		t.Fatalf("closing HTTP/3 must not change the HTTPS server status")
	}
}

func TestHttp3CloseWaitsForBridgeAcceptLoop(t *testing.T) {
	s := newTestHttp3Server(t, newTestDb(t))
	s.httpsServer = &http.Server{}
	s.Http3Bridge = true
	s.ticketKeys = [][32]byte{{1}}
	started := make(chan error, 1)
	go func() { started <- s.Start() }()
	time.Sleep(100 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after Close")
	}
}
