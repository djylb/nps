package legacy

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"encoding/hex"
	"github.com/djylb/netx"
	"github.com/djylb/tunnelwire"
	"golang.org/x/crypto/blake2b"
)

// The library's BLAKE2b, without a dependency, against the reference.
func TestKeyDigestMatchesBlake2b(t *testing.T) {
	buf := make([]byte, 600)
	for i := range buf {
		buf[i] = byte(i * 7)
	}
	for n := 0; n <= len(buf); n++ {
		want := blake2b.Sum256(buf[:n])
		if got := tunnelwire.KeyDigest(string(buf[:n])); got != hex.EncodeToString(want[:]) {
			t.Fatalf("length %d: %s", n, got)
		}
	}
}

func TestReadAckTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	err := ReadAck(client, 50*time.Millisecond)
	if err == nil {
		t.Fatal("ReadAck() error = nil, want timeout error")
	}
	if !netx.IsTimeout(err) {
		t.Fatalf("ReadAck() error = %v, want timeout-compatible error", err)
	}
}

func TestWriteAckAndReadAck(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteAck(server, 2*time.Second)
	}()

	if err := ReadAck(client, 2*time.Second); err != nil {
		t.Fatalf("ReadAck() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteAck() error = %v", err)
	}
}

func TestReadAckUnexpectedValue(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		_ = server.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := server.Write([]byte("NAK"))
		errCh <- err
	}()

	err := ReadAck(client, 2*time.Second)
	if err == nil {
		t.Fatal("ReadAck() error = nil, want non-nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadAck() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server write error = %v", err)
	}
}
