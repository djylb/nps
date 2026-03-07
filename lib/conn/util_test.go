package conn

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

)

type fakeNetError struct {
	msg       string
	temporary bool
	timeout   bool
}

func (e fakeNetError) Error() string   { return e.msg }
func (e fakeNetError) Temporary() bool { return e.temporary }
func (e fakeNetError) Timeout() bool   { return e.timeout }

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

func TestIsTempOrTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "net temporary", err: fakeNetError{msg: "temp", temporary: true}, want: true},
		{name: "net timeout", err: fakeNetError{msg: "timeout", timeout: true}, want: true},
		{name: "plain timeout text", err: io.EOF, want: false},
		{name: "plain timeout text", err: io.ErrNoProgress, want: false},
		{name: "net.Error without timeout flag", err: &net.DNSError{Err: "i/o timeout"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTempOrTimeout(tt.err); got != tt.want {
				t.Fatalf("IsTempOrTimeout(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestWriteACKAndReadACK(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

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
	defer server.Close()
	defer client.Close()

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
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("ReadACK() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server write error = %v", err)
	}
}
