package httpproxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
)

func writeTestCert(t *testing.T, dir, certName, keyName string) []byte {
	t.Helper()
	cert, err := tlsconn.NewSelfSigned(tlsconn.SelfSignedOptions{Hosts: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := tlsconn.EncodePEM(cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, certName), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, keyName), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return cert.Certificate[0]
}

func TestLoadCertReloadsChangedRelativeFiles(t *testing.T) {
	dir := t.TempDir()
	oldConf := common.ConfPath
	common.ConfPath = dir
	t.Cleanup(func() { common.ConfPath = oldConf })

	first := writeTestCert(t, dir, "host.pem", "host.key")
	s := &HttpsServer{cert: newCertFiles(servercfg.SSLConfig{CacheReload: 1})}
	cert, err := s.loadCert("host.pem", "host.key", "file")
	if err != nil {
		t.Fatal(err)
	}
	if string(cert.Certificate[0]) != string(first) {
		t.Fatal("loaded certificate is not the one in the files")
	}

	// Replace the pair the way renewal tools do: new files moved into place.
	second := writeTestCert(t, dir, "new.pem", "new.key")
	for _, p := range [][2]string{{"new.pem", "host.pem"}, {"new.key", "host.key"}} {
		if err := os.Rename(filepath.Join(dir, p[0]), filepath.Join(dir, p[1])); err != nil {
			t.Fatal(err)
		}
	}
	if cert, err = s.loadCert("host.pem", "host.key", "file"); err != nil || string(cert.Certificate[0]) != string(first) {
		t.Fatalf("certificate changed before the check interval: err %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if cert, err = s.loadCert("host.pem", "host.key", "file"); err != nil {
		t.Fatal(err)
	}
	if string(cert.Certificate[0]) != string(second) {
		t.Fatal("renewed certificate was not picked up")
	}
}

func TestLoadCertTextAndModes(t *testing.T) {
	s := &HttpsServer{cert: newCertFiles(servercfg.SSLConfig{})}
	if s.cert.CheckInterval >= 0 {
		t.Fatalf("ssl_cache_reload=0 checks files every %v", s.cert.CheckInterval)
	}
	cert, err := tlsconn.NewSelfSigned(tlsconn.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := tlsconn.EncodePEM(cert)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.loadCert(string(certPEM), string(keyPEM), "text")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.loadCert(string(certPEM), string(keyPEM), "text")
	if err != nil || first != second {
		t.Fatalf("inline certificate was not cached: err %v", err)
	}
	for _, mode := range []string{"empty", "invalid", ""} {
		if _, err := s.loadCert(string(certPEM), string(keyPEM), mode); err == nil {
			t.Fatalf("mode %q loaded a certificate", mode)
		}
	}
	if _, err := s.loadCert(filepath.Join(t.TempDir(), "missing.pem"), "missing.key", "file"); err == nil {
		t.Fatal("missing files loaded a certificate")
	}
}
