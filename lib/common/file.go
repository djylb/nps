package common

import (
	"crypto/tls"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		return filePath, nil
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	content, err := ReadAllFromFile(filePath)
	if err != nil || !strings.Contains(string(content), header) {
		return "", err
	}
	return string(content), nil
}

func LoadCertPair(certFile, keyFile string) (certContent, keyContent string, ok bool) {
	var wg sync.WaitGroup
	var certErr, keyErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		certContent, certErr = GetCertContent(certFile, "CERTIFICATE")
	}()
	go func() {
		defer wg.Done()
		keyContent, keyErr = GetCertContent(keyFile, "PRIVATE")
	}()
	wg.Wait()
	if certErr != nil || keyErr != nil || certContent == "" || keyContent == "" {
		return "", "", false
	}
	return certContent, keyContent, true
}

func LoadCert(certFile, keyFile string) (tls.Certificate, bool) {
	certContent, keyContent, ok := LoadCertPair(certFile, keyFile)
	if ok {
		certificate, err := tls.X509KeyPair([]byte(certContent), []byte(keyContent))
		if err == nil {
			return certificate, true
		}
	}
	return tls.Certificate{}, false
}

func GetCertType(s string) string {
	if s == "" {
		return "empty"
	}
	if strings.Contains(s, "-----BEGIN ") || strings.Contains(s, "\n") {
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
