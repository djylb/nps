package httpproxy

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type testCert struct {
	certPEM, keyPEM []byte
	der             []byte
}

func newTestCert(t *testing.T, names ...string) testCert {
	t.Helper()
	cert, err := tlsconn.NewSelfSigned(tlsconn.SelfSignedOptions{Hosts: names})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := tlsconn.EncodePEM(cert)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{certPEM: certPEM, keyPEM: keyPEM, der: cert.Certificate[0]}
}

// certSelectEnv is an isolated host database and an HTTP/3 server with a
// default certificate covering only default.test.
type certSelectEnv struct {
	t     *testing.T
	s     *Http3Server
	deflt testCert
}

func newCertSelectEnv(t *testing.T) *certSelectEnv {
	t.Helper()
	_ = file.GetDb()
	old := file.Db
	file.Db = &file.DbUtils{JsonDb: file.NewJsonDb(t.TempDir())}
	t.Cleanup(func() { file.Db = old })

	dir := t.TempDir()
	deflt := newTestCert(t, "default.test")
	certFile, keyFile := filepath.Join(dir, "default.crt"), filepath.Join(dir, "default.key")
	if err := os.WriteFile(certFile, deflt.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, deflt.keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestHttp3Server(t)
	s.cert = newCertFiles(servercfg.SSLConfig{})
	s.defaultCertFile, s.defaultKeyFile, s.hasDefaultCert = certFile, keyFile, true
	s.ticketKeys = [][32]byte{{1}}
	return &certSelectEnv{t: t, s: s, deflt: deflt}
}

func (e *certSelectEnv) addHost(id, clientID int, name, location string, cert *testCert) *file.Host {
	h := &file.Host{Id: id, Host: name, Location: location, Scheme: "all", Client: &file.Client{Id: clientID}}
	if cert != nil {
		h.CertFile, h.KeyFile, h.CertType = string(cert.certPEM), string(cert.keyPEM), "text"
	}
	file.Db.JsonDb.Hosts.Store(h.Id, h)
	file.HostIndex.Add(h.Host, h.Id)
	e.t.Cleanup(func() { file.HostIndex.Remove(h.Host, h.Id) })
	return h
}

func (e *certSelectEnv) expect(sni string, want testCert) {
	e.t.Helper()
	cfg, err := e.s.GetConfigForClient(&tls.ClientHelloInfo{ServerName: sni})
	if err != nil || cfg == nil || len(cfg.Certificates) != 1 {
		e.t.Fatalf("%s: config = %v, %v", sni, cfg, err)
	}
	if got := cfg.Certificates[0].Certificate[0]; !bytes.Equal(got, want.der) {
		e.t.Fatalf("%s: wrong certificate selected", sni)
	}
}

// expectNone checks that no certificate is offered for sni, so the default
// certificate does not leak to names it does not cover.
func (e *certSelectEnv) expectNone(sni string) {
	e.t.Helper()
	if cfg, err := e.s.GetConfigForClient(&tls.ClientHelloInfo{ServerName: sni}); cfg != nil || err != nil {
		e.t.Fatalf("%s: config = %v, %v, want none", sni, cfg, err)
	}
}

func TestCertSelectMultiSANCertCoversSiblingHost(t *testing.T) {
	e := newCertSelectEnv(t)
	multi := newTestCert(t, "a.example.com", "b.example.org")
	e.addHost(1, 1, "a.example.com", "/", &multi)
	e.addHost(2, 1, "b.example.org", "/", nil)
	e.expect("a.example.com", multi)
	e.expect("b.example.org", multi)
	e.expect("B.Example.Org.", multi)
}

func TestCertSelectWildcardDoesNotCoverApexOrDeeperNames(t *testing.T) {
	e := newCertSelectEnv(t)
	wild := newTestCert(t, "*.example.com")
	apex := newTestCert(t, "example.com")
	e.addHost(1, 1, "example.com", "/", &wild)
	e.addHost(2, 1, "apex.test", "/", &apex)
	e.addHost(3, 1, "x.a.example.com", "/", nil)
	e.addHost(4, 1, "sub.example.com", "/", nil)
	// The entry's own wildcard certificate does not cover the apex.
	e.expect("example.com", apex)
	// Neither certificate covers a name two labels below the apex, nor does
	// the default one, so none is offered.
	e.expectNone("x.a.example.com")
	// The apex certificate does not cover subdomains.
	e.expect("sub.example.com", wild)
}

func TestCertSelectExactBeatsWildcard(t *testing.T) {
	e := newCertSelectEnv(t)
	wild := newTestCert(t, "*.example.com")
	exact := newTestCert(t, "w.example.com")
	e.addHost(1, 1, "w.example.com", "/", nil)
	e.addHost(2, 1, "wild.test", "/", &wild)
	e.addHost(3, 1, "exact.test", "/", &exact)
	e.expect("w.example.com", exact)
}

func TestCertSelectOwnCertFirst(t *testing.T) {
	e := newCertSelectEnv(t)
	wild := newTestCert(t, "*.example.com")
	exact := newTestCert(t, "a.example.com")
	e.addHost(1, 1, "a.example.com", "/", &wild)
	e.addHost(2, 1, "other.test", "/", &exact)
	// The entry's own certificate covers the name, so it wins over a
	// sibling's more specific one.
	e.expect("a.example.com", wild)
}

func TestCertSelectEntryWithCertPreferredForSameName(t *testing.T) {
	e := newCertSelectEnv(t)
	own := newTestCert(t, "a.example.com")
	e.addHost(1, 1, "a.example.com", "/", nil)
	e.addHost(2, 2, "a.example.com", "/api", &own)
	e.addHost(3, 3, "*.example.com", "/", nil)
	e.addHost(4, 4, "B.Example.com", "/", &own)
	e.expect("a.example.com", own)
	e.expect("b.example.com", own)
}

func TestCertSelectNoCrossClientReuse(t *testing.T) {
	e := newCertSelectEnv(t)
	multi := newTestCert(t, "a.example.com", "b.example.org")
	e.addHost(1, 1, "a.example.com", "/", &multi)
	e.addHost(2, 2, "b.example.org", "/", nil)
	e.expectNone("b.example.org")
}

func TestCertSelectFallsBackToOwnCert(t *testing.T) {
	e := newCertSelectEnv(t)
	own := newTestCert(t, "other.example.com")
	e.addHost(1, 1, "a.example.com", "/", &own)
	e.expect("a.example.com", own)
	e.addHost(2, 1, "c.example.com", "/", nil)
	e.expectNone("c.example.com")
}

// newAutoSSLEnv is a certSelectEnv whose AutoSSL depends on the HTTPS port
// being 443, as configured in production, rather than being forced.
func newAutoSSLEnv(t *testing.T) *certSelectEnv {
	e := newCertSelectEnv(t)
	e.s.ForceAutoSsl = false
	e.s.HttpPort, e.s.HttpsPort = 8080, 443
	return e
}

func (e *certSelectEnv) expectAutoSSL(sni string) {
	e.t.Helper()
	cfg, err := e.s.GetConfigForClient(&tls.ClientHelloInfo{ServerName: sni})
	if err != nil || cfg != e.s.certMagicTls {
		e.t.Fatalf("%s: config = %p, %v, want the AutoSSL one", sni, cfg, err)
	}
}

func TestCertSelectAutoSSLEntry(t *testing.T) {
	e := newAutoSSLEnv(t)
	e.addHost(1, 1, "auto.example.com", "/", nil).AutoSSL = true
	e.expectAutoSSL("auto.example.com")
	e.expectAutoSSL("AUTO.example.com.")
}

func TestCertSelectAutoSSLNeedsStandardPort(t *testing.T) {
	e := newAutoSSLEnv(t)
	e.s.HttpsPort = 8443
	e.addHost(1, 1, "default.test", "/", nil).AutoSSL = true
	e.expect("default.test", e.deflt)
}

func TestCertSelectAutoSSLEntryPreferredForSameName(t *testing.T) {
	e := newAutoSSLEnv(t)
	e.addHost(1, 1, "auto.example.com", "/", nil)
	e.addHost(2, 2, "auto.example.com", "/api", nil).AutoSSL = true
	e.addHost(3, 3, "auto.example.com", "", nil)
	e.expectAutoSSL("auto.example.com")
}

func TestCertSelectAutoSSLWildcardEntry(t *testing.T) {
	e := newAutoSSLEnv(t)
	e.addHost(1, 1, "*.example.com", "/", nil).AutoSSL = true
	e.addHost(2, 2, "*.com", "/", &e.deflt)
	e.expectAutoSSL("a.example.com")
	e.expectAutoSSL("x.a.example.com")
}

func TestCertSelectAutoSSLByScheme(t *testing.T) {
	e := newAutoSSLEnv(t)
	for i, scheme := range []string{"https", "all"} {
		h := e.addHost(i+1, 1, scheme+".example.com", "/", nil)
		h.Scheme, h.AutoSSL = scheme, true
		e.expectAutoSSL(scheme + ".example.com")
	}
	// An HTTP-only entry never terminates TLS.
	h := e.addHost(3, 1, "http.example.com", "/", nil)
	h.Scheme, h.AutoSSL = "http", true
	e.expectNone("http.example.com")
}

func TestCertSelectNoEntryUsesDefaultCert(t *testing.T) {
	e := newCertSelectEnv(t)
	e.expect("default.test", e.deflt)
	e.expectNone("unknown.test")
	closed := e.addHost(1, 1, "closed.test", "/", nil)
	closed.IsClose = true
	e.expectNone("closed.test")

	e.s.hasDefaultCert = false
	if cfg, err := e.s.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "unknown.test"}); cfg != nil || err != nil {
		t.Fatalf("config = %v, %v, want none without a default certificate", cfg, err)
	}
}
