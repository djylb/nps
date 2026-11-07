package httpproxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/file"
)

func newTestHttp3Server(t *testing.T) *Http3Server {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	https := &HttpsServer{
		HttpServer:   &HttpServer{HttpProxy: &HttpProxy{ForceAutoSsl: true}},
		certMagicTls: &tls.Config{},
	}
	return NewHttp3Server(https, pc)
}

func TestHttp3SkipsTlsOffloadHost(t *testing.T) {
	t.Cleanup(file.ReplaceDb(&file.DbUtils{JsonDb: file.NewJsonDb(t.TempDir())}))

	host := &model.Host{Id: 1, Host: "offload.example", Scheme: "all", AutoSSL: true, Client: &model.Client{Id: 1}}
	file.GetDb().JsonDb.Hosts.Store(host.Id, host)
	file.HostIndex.Add(host.Host, host.Id)
	t.Cleanup(func() { file.HostIndex.Remove(host.Host, host.Id) })

	s := newTestHttp3Server(t)
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
	s := newTestHttp3Server(t)
	s.httpsStatus.Store(true)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !s.httpsStatus.Load() {
		t.Fatalf("closing HTTP/3 must not change the HTTPS server status")
	}
}

func TestHttp3CloseWaitsForBridgeAcceptLoop(t *testing.T) {
	s := newTestHttp3Server(t)
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
