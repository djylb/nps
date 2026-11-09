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
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/djylb/netx/tlsconn"
	"github.com/djylb/slogx"
)

var log = slogx.Module("crypt")

var (
	SkipVerify = false

	trusted   tlsconn.PinSet // bridge certificates of vkeyToPin
	trustedMu sync.Mutex
	vkeyToPin = make(map[string][sha256.Size]byte)
)

// Identity is the certificate a process presents in TLS, and the RSA key
// the web login encrypts credentials to.
type Identity struct {
	Cert   tls.Certificate
	rsaKey *rsa.PrivateKey
}

// NewIdentity returns the identity of custom, or of a fresh self-signed
// certificate when custom holds none. The RSA key is the certificate's own
// when it is an RSA key, a fresh one otherwise.
func NewIdentity(custom tls.Certificate) (*Identity, error) {
	id := &Identity{Cert: custom}
	if len(custom.Certificate) == 0 {
		var err error
		if id.Cert, err = newSelfSigned(gofakeit.DomainName()); err != nil {
			return nil, fmt.Errorf("create a self-signed certificate: %w", err)
		}
		log.Debug("self-signed certificate generated")
	}
	if key, ok := id.Cert.PrivateKey.(*rsa.PrivateKey); ok {
		id.rsaKey = key
		log.Debug("using the certificate rsa key")
	} else {
		var err error
		if id.rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			return nil, fmt.Errorf("generate a fallback RSA key: %w", err)
		}
		log.Debug("generated a fallback rsa key")
	}
	return id, nil
}

func GetFakeDomainName() string {
	return gofakeit.DomainName()
}

// TLSCert returns the certificate, none for a nil identity.
func (id *Identity) TLSCert() tls.Certificate {
	if id == nil {
		return tls.Certificate{}
	}
	return id.Cert
}

// Fingerprint returns the SHA-256 of the certificate's leaf
// (GetCertFingerprint).
func (id *Identity) Fingerprint() []byte {
	if id == nil {
		return nil
	}
	return GetCertFingerprint(id.Cert)
}

// RSAPublicKeyPEM returns the public RSA key the web login encrypts to.
func (id *Identity) RSAPublicKeyPEM() (string, error) {
	if id == nil || id.rsaKey == nil {
		return "", fmt.Errorf("RSA key not initialized")
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&id.rsaKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal RSA public key: %w", err)
	}
	pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}
	return string(pem.EncodeToMemory(pemBlock)), nil
}

func (id *Identity) decrypt(base64Cipher string) ([]byte, error) {
	if id == nil || id.rsaKey == nil {
		return nil, fmt.Errorf("RSA key not initialized")
	}
	// Decode base64
	cipherBytes, err := base64.StdEncoding.DecodeString(base64Cipher)
	if err != nil {
		return nil, fmt.Errorf("base64 decode error: %w", err)
	}
	plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, id.rsaKey, cipherBytes, nil)
	if err == nil {
		return plain, nil
	}
	// The web login and register pages encrypt with JSEncrypt, which only
	// produces PKCS#1 v1.5 ciphertext, so this fallback stays.
	//nolint:staticcheck // PKCS#1 v1.5 is required by the JSEncrypt login form
	//lint:ignore SA1019 PKCS#1 v1.5 is required by the JSEncrypt login form
	//noinspection GoDeprecation
	plain, legacyErr := rsa.DecryptPKCS1v15(rand.Reader, id.rsaKey, cipherBytes)
	if legacyErr != nil {
		return nil, fmt.Errorf("RSA decrypt error: oaep: %w; pkcs1v15: %w", err, legacyErr)
	}
	return plain, nil
}

type LoginPayload struct {
	Nonce     string `json:"n"`
	Timestamp int64  `json:"t"`
	Password  string `json:"p"`
}

// ParseLoginPayload decrypts the credentials a web login sent.
func (id *Identity) ParseLoginPayload(base64Cipher string) (*LoginPayload, error) {
	plain, err := id.decrypt(base64Cipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt login payload: %w", err)
	}
	var lp LoginPayload
	if err := json.Unmarshal(plain, &lp); err != nil {
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

// NewTlsServerConn returns a TLS server on conn that presents cert.
func NewTlsServerConn(conn net.Conn, cert tls.Certificate) net.Conn {
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
// fresh RSA key, which the web login also uses to decrypt credentials. The
// subject is empty and the serial number random, so the certificate says
// nothing about who made it; dnsName is its only name (as RFC 5280 wants for
// an empty subject, in a critical subject alternative name). Peers pin it
// by fingerprint or do not verify it.
func newSelfSigned(dnsName string) (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	serial.Add(serial, big.NewInt(1)) // positive
	notBefore := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{dnsName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}
