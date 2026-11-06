package httpproxy

import (
	"crypto/tls"
	"net"
	"testing"

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
	_ = file.GetDb()
	old := file.Db
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(t.TempDir())}
	t.Cleanup(func() { file.Db = old })

	host := &file.Host{Id: 1, Host: "offload.example", Scheme: "all", AutoSSL: true, Client: &file.Client{Id: 1}}
	file.Db.JsonDb.Hosts.Store(host.Id, host)
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
	s.httpsStatus = true
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !s.httpsStatus {
		t.Fatalf("closing HTTP/3 must not change the HTTPS server status")
	}
}
