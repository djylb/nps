package crypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/nps/lib/logs"
)

var (
	cert       tls.Certificate
	rsaKey     *rsa.PrivateKey
	SkipVerify = false

	trusted   tlsconn.PinSet // bridge certificates of vkeyToPin
	trustedMu sync.Mutex
	vkeyToPin = make(map[string][sha256.Size]byte)
)

func InitTls(customCert tls.Certificate) {
	if len(customCert.Certificate) > 0 {
		cert = customCert
		logs.Info("Custom certificate loaded successfully.")
	} else {
		commonName := gofakeit.DomainName()
		organization := gofakeit.Company()
		var err error
		if cert, err = newSelfSigned(commonName, organization); err != nil {
			logs.Error("Error initializing crypto certs %v", err)
		}
	}
	if key, ok := cert.PrivateKey.(*rsa.PrivateKey); ok {
		rsaKey = key
		logs.Info("Using RSA private key from TLS certificate.")
	} else {
		var err error
		rsaKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			logs.Error("Failed to generate fallback RSA key: %v", err)
		} else {
			logs.Info("Generated fallback RSA private key.")
		}
	}
}

func GetFakeDomainName() string {
	return gofakeit.DomainName()
}

func GetCert() tls.Certificate {
	return cert
}

func GetRSAPublicKeyPEM() (string, error) {
	if rsaKey == nil {
		return "", fmt.Errorf("RSA key not initialized")
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal RSA public key: %w", err)
	}
	pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}
	return string(pem.EncodeToMemory(pemBlock)), nil
}

func DecryptWithPrivateKey(base64Cipher string) ([]byte, error) {
	if rsaKey == nil {
		return nil, fmt.Errorf("RSA key not initialized")
	}
	// Decode base64
	cipherBytes, err := base64.StdEncoding.DecodeString(base64Cipher)
	if err != nil {
		return nil, fmt.Errorf("base64 decode error: %w", err)
	}
	plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, rsaKey, cipherBytes, nil)
	if err == nil {
		return plain, nil
	}
	// The web login and register pages encrypt with JSEncrypt, which only
	// produces PKCS#1 v1.5 ciphertext, so this fallback stays.
	//nolint:staticcheck // PKCS#1 v1.5 is required by the JSEncrypt login form
	//lint:ignore SA1019 PKCS#1 v1.5 is required by the JSEncrypt login form
	//noinspection GoDeprecation
	plain, legacyErr := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, cipherBytes)
	if legacyErr != nil {
		return nil, fmt.Errorf("RSA decrypt error: oaep: %w; pkcs1v15: %v", err, legacyErr)
	}
	return plain, nil
}

func DecryptStringWithPrivateKey(base64Cipher string) (string, error) {
	plain, err := DecryptWithPrivateKey(base64Cipher)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

type LoginPayload struct {
	Nonce     string `json:"n"`
	Timestamp int64  `json:"t"`
	Password  string `json:"p"`
}

func ParseLoginPayload(base64Cipher string) (*LoginPayload, error) {
	jsonStr, err := DecryptStringWithPrivateKey(base64Cipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt login payload: %w", err)
	}
	var lp LoginPayload
	if err := json.Unmarshal([]byte(jsonStr), &lp); err != nil {
		return nil, fmt.Errorf("unmarshal login payload: %w", err)
	}
	return &lp, nil
}

// GetCertFingerprint returns the SHA-256 of certificate's leaf, as the
// bridge sends it to the client during the handshake, or nil if it has none.
func GetCertFingerprint(certificate tls.Certificate) []byte {
	if len(certificate.Certificate) == 0 {
		return nil
	}
	sum := tlsconn.Fingerprint(certificate.Certificate[0])
	return sum[:]
}

// PinFingerprint returns the pin set holding the certificate with
// fingerprint, its SHA-256 (GetCertFingerprint), or an empty set, which
// trusts no certificate, if fingerprint is not a SHA-256.
func PinFingerprint(fingerprint []byte) *tlsconn.PinSet {
	pins := &tlsconn.PinSet{}
	if len(fingerprint) == sha256.Size {
		pins.Add([sha256.Size]byte(fingerprint))
	}
	return pins
}

// AddTrustedCert trusts the bridge certificate with fingerprint fp for the
// client with vkey, in place of the one trusted for it before.
func AddTrustedCert(vkey string, fp []byte) {
	trustedMu.Lock()
	defer trustedMu.Unlock()
	old, had := vkeyToPin[vkey]
	if len(fp) != sha256.Size {
		delete(vkeyToPin, vkey)
	} else {
		pin := [sha256.Size]byte(fp)
		if had && old == pin {
			return
		}
		vkeyToPin[vkey] = pin
		trusted.Add(pin)
	}
	if had && !pinInUse(old) {
		trusted.Remove(old)
	}
}

// pinInUse reports whether a vkey still trusts pin. trustedMu is held.
func pinInUse(pin [sha256.Size]byte) bool {
	for _, p := range vkeyToPin {
		if p == pin {
			return true
		}
	}
	return false
}

func NewTlsServerConn(conn net.Conn) net.Conn {
	return tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
}

// NewTlsClientConn returns a TLS client on conn that accepts only bridge
// certificates trusted by AddTrustedCert, or any if SkipVerify.
func NewTlsClientConn(conn net.Conn) net.Conn {
	if SkipVerify {
		return tls.Client(conn, &tls.Config{
			InsecureSkipVerify: true,
		})
	}
	return tls.Client(conn, trusted.ClientConfig(nil))
}

// NewPinnedTlsClientConfig returns a TLS client configuration that accepts
// only the server certificate with fingerprint (its SHA-256), as the server
// sent it, encrypted with the vkey, during the bridge handshake. It ignores
// SkipVerify: the fingerprint is authenticated by the vkey itself.
func NewPinnedTlsClientConfig(fingerprint []byte) *tls.Config {
	return PinFingerprint(fingerprint).ClientConfig(&tls.Config{MinVersion: tls.VersionTLS13})
}

// newSelfSigned returns a ten-year self-signed server certificate with a
// fresh RSA key, which the web login also uses to decrypt credentials.
func newSelfSigned(commonName, organization string) (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tlsconn.NewSelfSigned(tlsconn.SelfSignedOptions{
		Hosts:        []string{commonName},
		Organization: organization,
		ValidFor:     10 * 365 * 24 * time.Hour,
		Key:          key,
	})
}
