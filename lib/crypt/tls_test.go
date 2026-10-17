package crypt

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"slices"
	"testing"
	"time"
)

func TestPinFingerprint(t *testing.T) {
	sum := sha256.Sum256([]byte("test-cert-der"))
	if !PinFingerprint(sum[:]).Contains(sum) {
		t.Fatal("fingerprint of the certificate is not trusted")
	}
	other := sha256.Sum256([]byte("other"))
	for _, bad := range [][]byte{nil, sum[:len(sum)-1], append(sum[:], 0), other[:]} {
		if PinFingerprint(bad).Contains(sum) {
			t.Fatalf("fingerprint %x trusts the certificate", bad)
		}
	}
}

func TestGetCertFingerprintIsLeafSHA256(t *testing.T) {
	cert, err := newSelfSigned("example.com", "Example")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Certificate[0])
	if got := GetCertFingerprint(cert); !slices.Equal(got, sum[:]) {
		t.Fatalf("fingerprint %x, want %x", got, sum)
	}
	if GetCertFingerprint(tls.Certificate{}) != nil {
		t.Fatal("certificate without chain has a fingerprint")
	}
}

func TestInitTlsSelfSignedRSA(t *testing.T) {
	InitTls(tls.Certificate{})
	key, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("private key is %T, want *rsa.PrivateKey", cert.PrivateKey)
	}
	if key.N.BitLen() != 2048 {
		t.Fatalf("RSA key has %d bits", key.N.BitLen())
	}
	if rsaKey != key {
		t.Fatal("login RSA key is not the certificate's key")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName == "" || len(leaf.Subject.Organization) != 1 {
		t.Fatalf("subject %v lacks common name or organization", leaf.Subject)
	}
	if !slices.Equal(leaf.DNSNames, []string{leaf.Subject.CommonName}) {
		t.Fatalf("DNS names %v, want the common name", leaf.DNSNames)
	}
	if validFor := leaf.NotAfter.Sub(time.Now()); validFor < 9*365*24*time.Hour {
		t.Fatalf("certificate is valid for only %v", validFor)
	}
	if leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatalf("key usage %v", leaf.KeyUsage)
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatalf("extended key usage %v", leaf.ExtKeyUsage)
	}
	pubPEM, err := GetRSAPublicKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		t.Fatal("public key is not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !key.PublicKey.Equal(pub) {
		t.Fatal("public key PEM is not the certificate's key")
	}
}

func TestAddTrustedCertReplacesPin(t *testing.T) {
	fp1, fp2 := sha256.Sum256([]byte("one")), sha256.Sum256([]byte("two"))
	AddTrustedCert("vkey-a", fp1[:])
	AddTrustedCert("vkey-b", fp1[:])
	AddTrustedCert("vkey-a", fp2[:])
	if !trusted.Contains(fp1) || !trusted.Contains(fp2) {
		t.Fatal("pins in use are not trusted")
	}
	AddTrustedCert("vkey-b", fp2[:])
	if trusted.Contains(fp1) {
		t.Fatal("pin no vkey uses is still trusted")
	}
	AddTrustedCert("vkey-a", []byte("short"))
	AddTrustedCert("vkey-b", nil)
	if trusted.Contains(fp2) {
		t.Fatal("replaced pin is still trusted")
	}
}

func TestNewPinnedTlsClientConfig(t *testing.T) {
	serverCert, err := newSelfSigned("example.com", "Example")
	if err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("other"))
	for _, tc := range []struct {
		name string
		fp   []byte
		ok   bool
	}{
		{"pinned", GetCertFingerprint(serverCert), true},
		{"other", other[:], false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := net.Pipe()
			defer func() { _ = c.Close() }()
			srv := tls.Server(s, &tls.Config{Certificates: []tls.Certificate{serverCert}})
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = srv.Handshake()
				_ = srv.Close()
			}()
			err := tls.Client(c, NewPinnedTlsClientConfig(tc.fp)).Handshake()
			_ = c.Close()
			<-done
			if (err == nil) != tc.ok {
				t.Fatalf("handshake error %v, want success %v", err, tc.ok)
			}
		})
	}
}
