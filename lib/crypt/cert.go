package crypt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"

	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/slogx"
)

// GetCertContent returns filePath itself when it is PEM text containing
// header, or else the content of that file, relative to the run path.
func GetCertContent(filePath, header string) (string, error) {
	if filePath == "" || strings.Contains(filePath, header) {
		return textutil.NormalizeNewlines(filePath), nil
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(runenv.GetRunPath(), filePath)
	}
	content, err := os.ReadFile(filePath)
	if err != nil || !strings.Contains(string(content), header) {
		return "", err
	}
	return textutil.NormalizeNewlines(string(content)), nil
}

// LoadKeyPair loads a certificate and its key, each given as PEM text or as
// the path of a PEM file, relative to the run path.
func LoadKeyPair(certFile, keyFile string) (tls.Certificate, error) {
	certContent, err := GetCertContent(certFile, "CERTIFICATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	keyContent, err := GetCertContent(keyFile, "PRIVATE")
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tlsconn.ParseKeyPair([]byte(certContent), []byte(keyContent))
	if err != nil {
		return cert, err
	}
	var args []any
	if GetCertType(certFile) != "text" {
		args = append(args, "file", certFile)
	}
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	if leaf != nil {
		args = append(args, "until", leaf.NotAfter, "issuer", leaf.Issuer.CommonName)
	}
	slogx.Emit(context.Background(), log, &logevent.CertLoaded, args...)
	return cert, nil
}

// GetCertType reports whether s is "empty", PEM "text", an existing "file"
// or "invalid".
func GetCertType(s string) string {
	if s == "" {
		return "empty"
	}
	if strings.Contains(s, "-----BEGIN ") || strings.ContainsAny(s, "\r\n") {
		return "text"
	}
	if _, err := os.Stat(s); err == nil {
		return "file"
	}
	return "invalid"
}
