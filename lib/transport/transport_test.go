//go:build !windows
// +build !windows

package transport

import (
	"net"
	"testing"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}

	acceptCh := make(chan *net.TCPConn, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			errCh <- net.InvalidAddrError("accepted connection is not *net.TCPConn")
			_ = conn.Close()
			return
		}
		acceptCh <- tcpConn
	}()

	clientRaw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	client, ok := clientRaw.(*net.TCPConn)
	if !ok {
		_ = clientRaw.Close()
		t.Fatal("dialed connection is not *net.TCPConn")
	}

	var server *net.TCPConn
	select {
	case server = <-acceptCh:
	case err = <-errCh:
		_ = client.Close()
		t.Fatalf("accept tcp: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		_ = server.Close()
		_ = ln.Close()
	}
	return client, server, cleanup
}

func TestSetTcpKeepAliveParamsSuccess(t *testing.T) {
	client, _, cleanup := tcpPair(t)
	defer cleanup()

	if err := SetTcpKeepAliveParams(client, 10, 3, 5); err != nil {
		t.Fatalf("expected keepalive params set successfully, got error: %v", err)
	}
}

func TestSetTcpKeepAliveParamsOnClosedConn(t *testing.T) {
	client, _, cleanup := tcpPair(t)
	defer cleanup()

	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	if err := SetTcpKeepAliveParams(client, 10, 3, 5); err == nil {
		t.Fatal("expected error on closed connection, got nil")
	}
}
