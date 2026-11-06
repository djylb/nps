package common

import (
	"crypto/tls"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/djylb/netx/tlsconn"
)

// ReadAllFromFile Read file content by file path
func ReadAllFromFile(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func GetPath(filePath string) string {
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	path, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}
	return path
}

func GetCertContent(filePath, header string) (string, error) {
	if filePath == "" || strings.Contains(filePath, header) {
		return NormalizeNewlines(filePath), nil
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	content, err := ReadAllFromFile(filePath)
	if err != nil || !strings.Contains(string(content), header) {
		return "", err
	}
	return NormalizeNewlines(string(content)), nil
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
	return tlsconn.ParseKeyPair([]byte(certContent), []byte(keyContent))
}

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

// FileExists reports whether the named file or directory exists.
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}
