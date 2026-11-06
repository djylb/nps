package conn

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/netx"
)

func TestGetLenBytes(t *testing.T) {
	payload := []byte("hello")
	got, err := GetLenBytes(payload)
	if err != nil {
		t.Fatalf("GetLenBytes() error = %v", err)
	}

	if len(got) != 4+len(payload) {
		t.Fatalf("GetLenBytes() len = %d, want %d", len(got), 4+len(payload))
	}

	var n int32
	if err := binary.Read(bytes.NewReader(got[:4]), binary.LittleEndian, &n); err != nil {
		t.Fatalf("binary.Read(len) error = %v", err)
	}
	if int(n) != len(payload) {
		t.Fatalf("encoded length = %d, want %d", n, len(payload))
	}
	if !bytes.Equal(got[4:], payload) {
		t.Fatalf("payload = %q, want %q", got[4:], payload)
	}
}

func TestReadACKTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	err := ReadACK(client, 50*time.Millisecond)
	if err == nil {
		t.Fatal("ReadACK() error = nil, want timeout error")
	}
	if !netx.IsTimeout(err) {
		t.Fatalf("ReadACK() error = %v, want timeout-compatible error", err)
	}
}

func TestWriteACKAndReadACK(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteACK(server, 2*time.Second)
	}()

	if err := ReadACK(client, 2*time.Second); err != nil {
		t.Fatalf("ReadACK() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteACK() error = %v", err)
	}
}

func TestReadACKUnexpectedValue(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		_ = server.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := server.Write([]byte("NAK"))
		errCh <- err
	}()

	err := ReadACK(client, 2*time.Second)
	if err == nil {
		t.Fatal("ReadACK() error = nil, want non-nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadACK() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server write error = %v", err)
	}
}
