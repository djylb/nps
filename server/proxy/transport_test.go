package proxy

import (
	"errors"
	"net"
	"runtime"
	"testing"

	"github.com/djylb/netx/transparent"
)

func TestOriginalTargetDirectConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	server, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer func() { _ = server.Close() }()

	addr, err := originalTarget(server)
	if err == nil {
		t.Fatalf("originalTarget() = %q, want an error for a direct connection", addr)
	}
	switch runtime.GOOS {
	case "linux":
		if !errors.Is(err, errNotRedirected) {
			t.Fatalf("originalTarget() error = %v, want errNotRedirected", err)
		}
	case "darwin", "freebsd":
		// Without root, the pf lookup itself fails; with it, the
		// connection is reported as not redirected.
	default:
		if !errors.Is(err, transparent.ErrOriginalDestinationUnsupported) {
			t.Fatalf("originalTarget() error = %v, want ErrOriginalDestinationUnsupported", err)
		}
	}
}

func TestOriginalTargetPipe(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()

	if addr, err := originalTarget(b); err == nil {
		t.Fatalf("originalTarget() = %q, want an error for a connection without a socket", addr)
	}
}
