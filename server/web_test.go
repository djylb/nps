package server

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type stubAddr string

func (a stubAddr) Network() string { return "stub" }
func (a stubAddr) String() string  { return string(a) }

type stubListener struct {
	closed chan struct{}
	once   sync.Once
}

func newStubListener() *stubListener {
	return &stubListener{closed: make(chan struct{})}
}

func (l *stubListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *stubListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *stubListener) Addr() net.Addr {
	return stubAddr("stub-listener")
}

func TestServeHTTPListenersClosesPeersOnFirstExit(t *testing.T) {
	primary := newStubListener()
	secondary := newStubListener()
	expectedErr := errors.New("primary serve failed")

	err := serveHTTPListeners(
		httpServeTarget{
			listener: primary,
			serve: func(net.Listener) error {
				return expectedErr
			},
		},
		httpServeTarget{
			listener: secondary,
			serve: func(net.Listener) error {
				<-secondary.closed
				return net.ErrClosed
			},
		},
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("serveHTTPListeners() error = %v, want %v", err, expectedErr)
	}

	select {
	case <-primary.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("primary listener was not closed after serve exit")
	}
	select {
	case <-secondary.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("secondary listener was not closed after peer serve exit")
	}
}
