package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func newTestHTTPProxyServer(t *testing.T) *TunnelModeServer {
	t.Helper()
	task := &file.Tunnel{
		Mode: "httpProxy",
		Client: &file.Client{
			Id:   1,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{},
	}
	bridge := &localDialBridge{links: make(chan *conn.Link, 16)}
	return NewTunnelModeServer(ProcessHttp, bridge, task, false)
}

// TestProcessHttpServesKeepAliveAndReturnsOnClose checks the lifecycle of the
// forward proxy's per-connection http.Server: it keeps the client connection
// open across requests and returns as soon as the client goes away, closing
// its backend connections.
func TestProcessHttpServesKeepAliveAndReturnsOnClose(t *testing.T) {
	var mu sync.Mutex
	backendStates := map[net.Conn]http.ConnState{}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello "+r.URL.Path)
	}))
	backend.Config.ConnState = func(c net.Conn, state http.ConnState) {
		mu.Lock()
		backendStates[c] = state
		mu.Unlock()
	}
	backend.Start()
	defer backend.Close()
	backendHost := strings.TrimPrefix(backend.URL, "http://")

	s := newTestHTTPProxyServer(t)
	client, server := tcpConnPair(t)
	done := make(chan error, 1)
	go func() { done <- ProcessHttp(conn.NewConn(server), s) }()

	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(client)
	for _, path := range []string{"/one", "/two"} {
		req := "GET http://" + backendHost + path + " HTTP/1.1\r\nHost: " + backendHost + "\r\n\r\n"
		if _, err := io.WriteString(client, req); err != nil {
			t.Fatalf("write request %s: %v", path, err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read response %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != "hello "+path {
			t.Fatalf("response %s = %d %q, %v", path, resp.StatusCode, body, err)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("ProcessHttp returned while the client was still connected: %v", err)
	default:
	}

	_ = client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessHttp() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessHttp did not return after the client closed")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		open := 0
		for _, state := range backendStates {
			if state != http.StateClosed {
				open++
			}
		}
		total := len(backendStates)
		mu.Unlock()
		if total > 0 && open == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend connections still open after ProcessHttp returned: %d of %d", open, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestProcessHttpReturnsWhenServerClosesConn covers a response that ends the
// connection: the proxy closes it and ProcessHttp returns without waiting
// for its idle timer.
func TestProcessHttpReturnsWhenServerClosesConn(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "bye")
	}))
	defer backend.Close()
	backendHost := strings.TrimPrefix(backend.URL, "http://")

	s := newTestHTTPProxyServer(t)
	client, server := tcpConnPair(t)
	done := make(chan error, 1)
	go func() { done <- ProcessHttp(conn.NewConn(server), s) }()

	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	req := "GET http://" + backendHost + "/ HTTP/1.1\r\nHost: " + backendHost + "\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(client, req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	br := bufio.NewReader(client)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "bye" {
		t.Fatalf("body = %q, want bye", body)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("read after Connection: close = %v, want EOF", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessHttp() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessHttp did not return after closing the connection")
	}
}
