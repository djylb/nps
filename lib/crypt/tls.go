package crypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"sync"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/djylb/nps/lib/logs"
)

var (
	cert       tls.Certificate
	rsaKey     *rsa.PrivateKey
	trustedSet sync.Map // key:string -> struct{}
	vkeyToFp   sync.Map // key:vkey(string) -> fpHex(string)
	SkipVerify = false
	tlsCfg     *tls.Config
)

func InitTls(customCert tls.Certificate) {
	if len(customCert.Certificate) > 0 {
		cert = customCert
		logs.Info("Custom certificate loaded successfully.")
	} else {
		commonName := gofakeit.DomainName()
		organization := gofakeit.Company()
		c, k, err := generateKeyPair(commonName, organization)
		if err == nil {
			cert, err = tls.X509KeyPair(c, k)
		}
		if err != nil {
			logs.Error("Error initializing crypto certs %v", err)
		}
	}
	tlsCfg = &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3", "h2", "http/1.1"},
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

func GetCertCfg() *tls.Config {
	return tlsCfg
}

func GetPublicKeyPEM() (string, error) {
	if len(cert.Certificate) == 0 {
		return "", fmt.Errorf("no certificate available")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %w", err)
	}
	pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}
	return string(pem.EncodeToMemory(pemBlock)), nil
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
	// Decrypt using PKCS#1 v1.5
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, rsaKey, cipherBytes)
	if err != nil {
		return nil, fmt.Errorf("RSA decrypt error: %w", err)
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

func GetCertFingerprint(certificate tls.Certificate) []byte {
	if len(certificate.Certificate) == 0 {
		return nil
	}
	sum := sha256.Sum256(certificate.Certificate[0])
	return sum[:]
}

func AddTrustedCert(vkey string, fp []byte) {
	hexFp := hex.EncodeToString(fp)
	if oldRaw, loaded := vkeyToFp.Load(vkey); loaded {
		oldFp := oldRaw.(string)
		if oldFp == hexFp {
			return
		}
		trustedSet.Delete(oldFp)
	}
	vkeyToFp.Store(vkey, hexFp)
	trustedSet.LoadOrStore(hexFp, struct{}{})
}

func NewTlsServerConn(conn net.Conn) net.Conn {
	var err error
	if err != nil {
		logs.Error("%v", err)
		os.Exit(0)
		return nil
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	return tls.Server(conn, config)
}

func NewTlsClientConn(conn net.Conn) net.Conn {
	if SkipVerify {
		return tls.Client(conn, &tls.Config{
			InsecureSkipVerify: true,
		})
	}

	return tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("no server certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			fp := hex.EncodeToString(sum[:])
			if _, ok := trustedSet.Load(fp); ok {
				return nil
			}
			return errors.New("untrusted server certificate")
		},
	})
}

func generateKeyPair(commonName, organization string) (rawCert, rawKey []byte, err error) {
	// Create private key and self-signed certificate
	// Adapted from https://golang.org/src/crypto/tls/generate_cert.go

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}
	validFor := time.Hour * 24 * 365 * 10 // ten years
	notBefore := time.Now()
	notAfter := notBefore.Add(validFor)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{organization},
			CommonName:   commonName,
			Country:      []string{"US"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return
	}

	rawCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	rawKey = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return
}
