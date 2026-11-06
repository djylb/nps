package httpproxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestWriteHandshakeRefusalRelaysPlainResponse(t *testing.T) {
	raw := "HTTP/1.1 403 Forbidden\r\n" +
		"Content-Length: 6\r\n" +
		"Connection: keep-alive, X-Hop\r\n" +
		"X-Hop: 1\r\n" +
		"Upgrade: websocket\r\n" +
		"X-Backend: yes\r\n\r\n" +
		"denied"
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), req)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}

	s := &HttpServer{HttpProxy: &HttpProxy{}}
	rec := httptest.NewRecorder()
	s.writeHandshakeRefusal(rec, resp, &file.Host{})

	got := rec.Result()
	body, _ := io.ReadAll(got.Body)
	if got.StatusCode != http.StatusForbidden || string(body) != "denied" {
		t.Fatalf("got status %d body %q", got.StatusCode, body)
	}
	if got.Header.Get("Connection") != "close" {
		t.Fatalf("Connection = %q, want close", got.Header.Get("Connection"))
	}
	if got.Header.Get("Upgrade") != "" || got.Header.Get("X-Hop") != "" {
		t.Fatalf("hop-by-hop headers relayed: %v", got.Header)
	}
	if got.Header.Get("X-Backend") != "yes" {
		t.Fatalf("end-to-end header dropped: %v", got.Header)
	}
}

func TestRelayHandshakeRefusalStreamsBodyBeforeClosingBackend(t *testing.T) {
	backend, peer := net.Pipe()
	body := strings.Repeat("x", 64<<10)
	go func() {
		_, _ = io.WriteString(peer, "HTTP/1.1 400 Bad Request\r\nContent-Length: "+
			strconv.Itoa(len(body))+"\r\n\r\n"+body)
	}()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	resp, err := http.ReadResponse(bufio.NewReader(backend), req)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}

	s := &HttpServer{HttpProxy: &HttpProxy{ResponseHeaderTimeout: 5 * time.Second}}
	rec := httptest.NewRecorder()
	s.relayHandshakeRefusal(rec, resp, &file.Host{}, backend)

	if got := rec.Body.Len(); rec.Code != http.StatusBadRequest || got != len(body) {
		t.Fatalf("got status %d, body %d bytes, want 400 and %d bytes", rec.Code, got, len(body))
	}
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("backend not closed after relay: %v", err)
	}
}
