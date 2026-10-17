package conn

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestDialVirtualReportsAddressesAndCarriesData(t *testing.T) {
	local := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 8024}
	l := NewVirtualListener(local)
	defer func() { _ = l.Close() }()

	c, err := DialVirtual(context.Background(), l, "192.0.2.7:4321")
	if err != nil {
		t.Fatalf("DialVirtual() error = %v", err)
	}
	defer func() { _ = c.Close() }()
	accepted, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	defer func() { _ = accepted.Close() }()

	if got := accepted.RemoteAddr().String(); got != "192.0.2.7:4321" {
		t.Fatalf("accepted RemoteAddr = %s, want 192.0.2.7:4321", got)
	}
	if got := accepted.LocalAddr().String(); got != local.String() {
		t.Fatalf("accepted LocalAddr = %s, want %s", got, local)
	}
	if got := c.RemoteAddr().String(); got != local.String() {
		t.Fatalf("dialed RemoteAddr = %s, want %s", got, local)
	}

	go func() { _, _ = c.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(accepted, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v; want ping", buf, err)
	}
}

func TestNewVirtualListenerDefaultsToLocalAddr(t *testing.T) {
	l := NewVirtualListener(nil)
	defer func() { _ = l.Close() }()
	if l.Addr() != LocalTCPAddr {
		t.Fatalf("Addr() = %v, want %v", l.Addr(), LocalTCPAddr)
	}
}

func TestDialVirtualRejectsBadRemote(t *testing.T) {
	l := NewVirtualListener(nil)
	defer func() { _ = l.Close() }()
	for _, remote := range []string{"", "no-port"} {
		if c, err := DialVirtual(context.Background(), l, remote); err == nil {
			_ = c.Close()
			t.Fatalf("DialVirtual(%q) succeeded, want error", remote)
		}
	}
}

func TestVirtualListenerClose(t *testing.T) {
	l := NewVirtualListener(nil)

	queued, err := DialVirtual(context.Background(), l, "127.0.0.1:1000")
	if err != nil {
		t.Fatalf("DialVirtual() error = %v", err)
	}
	defer func() { _ = queued.Close() }()

	acceptErr := make(chan error, 1)
	go func() {
		// The first Accept takes the queued connection; the second blocks
		// until Close.
		c, err := l.Accept()
		if err == nil {
			_ = c.Close()
			_, err = l.Accept()
		}
		acceptErr <- err
	}()
	// Wait until the queued connection has been taken and closed.
	_ = queued.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := queued.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("queued conn read error = %v, want EOF", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-acceptErr:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept() after Close error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return after Close")
	}

	if c, err := DialVirtual(context.Background(), l, "127.0.0.1:1001"); !errors.Is(err, net.ErrClosed) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("DialVirtual() after Close error = %v, want net.ErrClosed", err)
	}

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	ServeVirtual(l, server)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("ServeVirtual on a closed listener left the conn open: read error = %v", err)
	}
}

func TestVirtualListenerCloseClosesQueuedConns(t *testing.T) {
	l := NewVirtualListener(nil)
	c, err := DialVirtual(context.Background(), l, "127.0.0.1:1000")
	if err != nil {
		t.Fatalf("DialVirtual() error = %v", err)
	}
	defer func() { _ = c.Close() }()
	_ = l.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read error = %v, want EOF from the closed server end", err)
	}
}

func TestDialVirtualWaitsForQueueSpaceUntilContextDone(t *testing.T) {
	l := NewVirtualListener(nil)
	defer func() { _ = l.Close() }()
	for i := 0; i < virtualQueue; i++ {
		client, server := net.Pipe()
		defer func() { _ = client.Close() }()
		if !l.TryDeliver(server) {
			t.Fatalf("TryDeliver #%d failed before the queue was full", i)
		}
	}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	ServeVirtual(l, server) // queue full: closed instead of queued
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("ServeVirtual on a full queue left the conn open: read error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if c, err := DialVirtual(ctx, l, "127.0.0.1:1000"); !errors.Is(err, context.DeadlineExceeded) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("DialVirtual() on a full queue error = %v, want context.DeadlineExceeded", err)
	}
}
