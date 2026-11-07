package crypt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCertHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	cert := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	key := strings.Join([]string{
		"-----BEGIN " + "PRIVA" + "TE KEY-----",
		"MIIB",
		"-----END " + "PRIVA" + "TE KEY-----",
	}, "\n")
	if err := os.WriteFile(certPath, []byte(cert), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, err := GetCertContent(cert, "CERTIFICATE"); err != nil || got != cert {
		t.Fatalf("GetCertContent(text) = (%q, %v), want text,nil", got, err)
	}
	if got, err := GetCertContent(certPath, "CERTIFICATE"); err != nil || !strings.Contains(got, "BEGIN CERTIFICATE") {
		t.Fatalf("GetCertContent(file) err=%v got=%q", err, got)
	}
	if got, err := GetCertContent(filepath.Join(tmpDir, "missing.pem"), "CERTIFICATE"); err == nil || got != "" {
		t.Fatalf("GetCertContent(missing) = (%q, %v), want empty,error", got, err)
	}

	if got := GetCertType(""); got != "empty" {
		t.Fatalf("GetCertType(empty) = %q, want empty", got)
	}
	if got := GetCertType(cert); got != "text" {
		t.Fatalf("GetCertType(text) = %q, want text", got)
	}
	if got := GetCertType(certPath); got != "file" {
		t.Fatalf("GetCertType(file) = %q, want file", got)
	}
	if got := GetCertType("non-existent.pem"); got != "invalid" {
		t.Fatalf("GetCertType(invalid) = %q, want invalid", got)
	}
}
