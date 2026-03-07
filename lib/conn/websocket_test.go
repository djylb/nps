package conn

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWsConnReadStreamsAcrossCalls(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	msg := []byte("0123456789")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer func() { _ = ws.Close() }()
		_ = ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err = ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("write message failed: %v", err)
		}
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()
	dialer := websocket.Dialer{}
	url := "ws" + server.URL[len("http"):]
	client, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = client.Close() }()
	conn := NewWsConn(client)
	buf := make([]byte, 4)

	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if got := string(buf[:n]); got != "0123" {
		t.Fatalf("first read mismatch: got %q", got)
	}

	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if got := string(buf[:n]); got != "4567" {
		t.Fatalf("second read mismatch: got %q", got)
	}

	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("third read failed: %v", err)
	}
	if got := string(buf[:n]); got != "89" {
		t.Fatalf("third read mismatch: got %q", got)
	}

	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected EOF after close frame")
	}
	if err != io.EOF && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("expected EOF or close-normal error, got: %v", err)
	}
}
