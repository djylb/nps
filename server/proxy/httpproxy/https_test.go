package httpproxy

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func TestHttpsListenerPushAfterCloseDoesNotPanic(t *testing.T) {
	l := NewHttpsListener(nil)
	_ = l.Close()

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		l.push(tls.Server(server, &tls.Config{}))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a closed listener")
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("pushed connection was not closed")
	}

	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() error = %v, want net.ErrClosed", err)
	}
}
