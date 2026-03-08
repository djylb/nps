package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestBasicAuth(t *testing.T) {
	t.Parallel()

	users := map[string]string{"demo": "secret"}
	h := basicAuth(users, "WebDAV", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name           string
		authHeader     string
		wantStatusCode int
		wantChallenge  bool
	}{
		{name: "missing auth", wantStatusCode: http.StatusUnauthorized, wantChallenge: true},
		{name: "invalid base64", authHeader: "Basic !!!", wantStatusCode: http.StatusUnauthorized},
		{name: "wrong password", authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte("demo:bad")), wantStatusCode: http.StatusUnauthorized, wantChallenge: true},
		{name: "valid credential", authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte("demo:secret")), wantStatusCode: http.StatusNoContent},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			if err != nil {
				t.Fatalf("new request failed: %v", err)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := newTestResponseRecorder()
			h.ServeHTTP(rr, req)
			if rr.statusCode != tt.wantStatusCode {
				t.Fatalf("status code mismatch, got=%d want=%d", rr.statusCode, tt.wantStatusCode)
			}
			if got := rr.header.Get("WWW-Authenticate"); (got != "") != tt.wantChallenge {
				t.Fatalf("WWW-Authenticate mismatch, got=%q wantChallenge=%v", got, tt.wantChallenge)
			}
		})
	}
}

func TestReadOnly(t *testing.T) {
	t.Parallel()

	called := false
	h := readOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead, "PROPFIND"} {
		req, err := http.NewRequest(method, "/", nil)
		if err != nil {
			t.Fatalf("new request failed: %v", err)
		}
		rr := newTestResponseRecorder()
		h.ServeHTTP(rr, req)
		if rr.statusCode != http.StatusNoContent {
			t.Fatalf("method=%s got status=%d", method, rr.statusCode)
		}
	}
	if !called {
		t.Fatal("expected next handler to be called for allowed methods")
	}

	req, err := http.NewRequest(http.MethodPost, "/", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	rr := newTestResponseRecorder()
	h.ServeHTTP(rr, req)
	if rr.statusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post should be rejected, got status=%d", rr.statusCode)
	}
	if got := rr.header.Get("Allow"); got != "GET, HEAD, PROPFIND" {
		t.Fatalf("allow header mismatch, got=%q", got)
	}
}

func TestFileServerManagerStartAndCloseAll(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello nps"), 0o600); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	fsm := NewFileServerManager(context.Background())
	tunnel := &file.Tunnel{
		ServerIp:  "127.0.0.1",
		Port:      18080,
		Ports:     "18080",
		Mode:      "file",
		LocalPath: root,
		StripPre:  "/files",
		ReadOnly:  true,
	}
	fsm.StartFileServer(tunnel, "vkey")
	t.Cleanup(fsm.CloseAll)

	listener, ok := waitListener(fsm, 2*time.Second)
	if !ok {
		t.Fatal("file server listener was not registered")
	}

	resp := doVirtualRequest(t, listener, "GET /files/hello.txt HTTP/1.1\r\nHost: local\r\n\r\n")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status mismatch, got=%d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "hello nps" {
		t.Fatalf("GET body mismatch, got=%q", string(body))
	}

	resp = doVirtualRequest(t, listener, "POST /files/hello.txt HTTP/1.1\r\nHost: local\r\nContent-Length: 0\r\n\r\n")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status mismatch, got=%d", resp.StatusCode)
	}

	fsm.CloseAll()
	if _, err := listener.DialVirtual("127.0.0.1:12345"); err == nil {
		t.Fatal("expected listener dial to fail after CloseAll")
	}
}

func doVirtualRequest(t *testing.T, listener *conn.VirtualListener, raw string) *http.Response {
	t.Helper()
	c, err := listener.DialVirtual("127.0.0.1:9000")
	if err != nil {
		t.Fatalf("dial virtual failed: %v", err)
	}
	defer func() { _ = c.Close() }()

	if _, err = c.Write([]byte(raw)); err != nil {
		t.Fatalf("write request failed: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	return resp
}

func waitListener(fsm *FileServerManager, timeout time.Duration) (*conn.VirtualListener, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fsm.mu.Lock()
		for _, server := range fsm.servers {
			listener := server.listener
			fsm.mu.Unlock()
			return listener, true
		}
		fsm.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil, false
}

type testResponseRecorder struct {
	header     http.Header
	statusCode int
}

func newTestResponseRecorder() *testResponseRecorder {
	return &testResponseRecorder{header: make(http.Header)}
}

func (r *testResponseRecorder) Header() http.Header { return r.header }

func (r *testResponseRecorder) Write(body []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return len(body), nil
}

func (r *testResponseRecorder) WriteHeader(statusCode int) { r.statusCode = statusCode }
