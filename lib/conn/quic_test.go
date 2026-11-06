package conn

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func testQuicCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// Data written right before Close must still reach the peer.
func TestQuicAutoCloseConnDeliversDataBeforeClosing(t *testing.T) {
	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{testQuicCert(t)}, NextProtos: []string{"t"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	payload := bytes.Repeat([]byte("0123456789abcdef"), 1<<16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		sess, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		stream, err := sess.AcceptStream(ctx)
		if err != nil {
			return
		}
		c := NewQuicAutoCloseConn(stream, sess)
		_, _ = c.Write(payload)
		_ = c.Close()
	}()

	sess, err := quic.DialAddr(ctx, ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"t"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.CloseWithError(0, "") }()
	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read after %d bytes: %v", len(got), err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}
}
