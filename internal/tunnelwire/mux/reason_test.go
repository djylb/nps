package mux

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMuxAcceptReturnsCloseReason(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	m := NewMux(server, "tcp", 30, true)
	reason := "read session unpack failed: kind=rst err=\"connection reset by peer\""
	if err := m.closeWithReason(reason); err != nil {
		t.Fatalf("closeWithReason() error = %v", err)
	}

	_, err := m.Accept()
	if err == nil {
		t.Fatal("Accept() error = nil, want close reason")
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("Accept() error = %q, want reason %q", err.Error(), reason)
	}
	if got := m.CloseReason(); got != reason {
		t.Fatalf("CloseReason() = %q, want %q", got, reason)
	}
}

// The peer does not answer here, so NewConnTimeout ends on its own timeout.
func TestNewConnTimeoutHonoursTimeout(t *testing.T) {
	server, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	m := NewMux(server, "tcp", 30, true)
	defer func() { _ = m.Close() }()
	start := time.Now()
	if _, err := m.NewConnTimeout(100 * time.Millisecond); err == nil {
		t.Fatal("NewConnTimeout = nil, want a timeout")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("NewConnTimeout took %v", d)
	}
	if d := m.defaultNewConnTimeout(); d != 15*time.Second {
		t.Fatalf("default timeout = %v, want 15s for tcp with a 30s disconnect timeout", d)
	}
}
