package mux

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestMuxPair(t *testing.T) (client, server *Mux) {
	t.Helper()
	a, b := net.Pipe()
	client = NewMux(a, "tcp", 30, true)
	server = NewMux(b, "tcp", 30, false)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

func openTestStream(t *testing.T, client, server *Mux) (local, remote *Conn) {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		c, err := server.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- c
	}()
	local, err := client.NewConn()
	if err != nil {
		t.Fatalf("NewConn() error = %v", err)
	}
	select {
	case c := <-accepted:
		remote = c.(*Conn)
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Accept() did not return")
	}
	t.Cleanup(func() {
		_ = local.Close()
		_ = remote.Close()
	})
	return local, remote
}

func requireTimeout(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v, want os.ErrDeadlineExceeded", err)
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("error = %v, want net.Error with Timeout() == true", err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error message %q does not mention timeout", err.Error())
	}
}

func readFullWithin(t *testing.T, c net.Conn, n int, d time.Duration) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("ReadFull(%d) error = %v", n, err)
	}
	return buf
}

func waitQueued(t *testing.T, c *Conn, n uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.receiveWindow.bufQueue.Len() < n {
		if time.Now().After(deadline) {
			t.Fatalf("queued %d bytes, want %d", c.receiveWindow.bufQueue.Len(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnPastDeadlineTimesOutImmediately(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, _ := openTestStream(t, client, server)

	_ = local.SetDeadline(time.Now().Add(-time.Second))
	start := time.Now()
	_, err := local.Read(make([]byte, 16))
	requireTimeout(t, err)
	_, err = local.Write([]byte("data"))
	requireTimeout(t, err)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("past deadline took %v, want immediate", elapsed)
	}
}

func TestConnSetReadDeadlineWakesBlockedRead(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, _ := openTestStream(t, client, server)

	errCh := make(chan error, 1)
	go func() {
		_, err := local.Read(make([]byte, 16))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = local.SetReadDeadline(time.Now())
	select {
	case err := <-errCh:
		requireTimeout(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Read was not woken by SetReadDeadline")
	}
}

func TestConnReadTimeoutKeepsStreamAndData(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, remote := openTestStream(t, client, server)
	other, otherRemote := openTestStream(t, client, server)

	// data queued before the deadline survives a timed out Read
	if _, err := remote.Write([]byte("abc")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	waitQueued(t, local, 3)
	_ = local.SetReadDeadline(time.Now().Add(-time.Second))
	for i := 0; i < 2; i++ {
		_, err := local.Read(make([]byte, 16))
		requireTimeout(t, err)
	}
	_ = local.SetReadDeadline(time.Time{})
	if got := readFullWithin(t, local, 3, 2*time.Second); string(got) != "abc" {
		t.Fatalf("Read() = %q, want %q", got, "abc")
	}

	// a Read that times out while waiting leaves the wait flag set; frames
	// arriving afterwards must not stall the mux read loop
	_ = local.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	_, err := local.Read(make([]byte, 16))
	requireTimeout(t, err)
	_, err = local.Read(make([]byte, 16))
	requireTimeout(t, err)

	if _, err := remote.Write([]byte("def")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := otherRemote.Write([]byte("xyz")); err != nil {
		t.Fatalf("Write() other error = %v", err)
	}
	if got := readFullWithin(t, other, 3, 2*time.Second); string(got) != "xyz" {
		t.Fatalf("other Read() = %q, want %q", got, "xyz")
	}
	_ = local.SetReadDeadline(time.Time{})
	if got := readFullWithin(t, local, 3, 2*time.Second); string(got) != "def" {
		t.Fatalf("Read() = %q, want %q", got, "def")
	}
}

func TestConnReadAfterEOFDoesNotPanic(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, remote := openTestStream(t, client, server)

	if _, err := remote.Write([]byte("last")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_ = remote.Close()
	if got := readFullWithin(t, local, 4, 2*time.Second); string(got) != "last" {
		t.Fatalf("Read() = %q, want %q", got, "last")
	}
	_ = local.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 3; i++ {
		if _, err := local.Read(make([]byte, 16)); err != io.EOF {
			t.Fatalf("Read() #%d error = %v, want io.EOF", i, err)
		}
	}
	_ = local.Close()
	if _, err := local.Read(make([]byte, 16)); err == nil {
		t.Fatal("Read() after Close error = nil")
	}
}

func TestConnSetWriteDeadlineWakesBlockedWrite(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, remote := openTestStream(t, client, server)

	payload := make([]byte, 4<<20) // far larger than the initial window
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	type result struct {
		n   int
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		n, err := local.Write(payload)
		resCh <- result{n, err}
	}()
	time.Sleep(100 * time.Millisecond)
	_ = local.SetWriteDeadline(time.Now())
	var res result
	select {
	case res = <-resCh:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Write was not woken by SetWriteDeadline")
	}
	requireTimeout(t, res.err)
	if res.n <= 0 || res.n >= len(payload) {
		t.Fatalf("Write() n = %d, want partial write", res.n)
	}

	// exactly the reported bytes arrive, then the stream keeps working
	got := readFullWithin(t, remote, res.n, 5*time.Second)
	if !bytes.Equal(got, payload[:res.n]) {
		t.Fatal("received data does not match the reported partial write")
	}
	_ = local.SetWriteDeadline(time.Time{})
	if _, err := local.Write([]byte("tail")); err != nil {
		t.Fatalf("Write() after timeout error = %v", err)
	}
	if got := readFullWithin(t, remote, 4, 2*time.Second); string(got) != "tail" {
		t.Fatalf("Read() = %q, want %q", got, "tail")
	}
}

func TestWindowCloseConcurrent(t *testing.T) {
	w := new(window)
	w.New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.CloseWindow()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent CloseWindow blocked")
	}
	select {
	case <-w.closeOpCh:
	default:
		t.Fatal("closeOpCh not closed")
	}
	if !w.closed() {
		t.Fatal("closed() = false after CloseWindow")
	}
}

func TestConnCloseConcurrentWithBlockedIO(t *testing.T) {
	client, server := newTestMuxPair(t)
	local, _ := openTestStream(t, client, server)

	errs := make(chan error, 2)
	go func() {
		_, err := local.Read(make([]byte, 16))
		errs <- err
	}()
	go func() {
		_, err := local.Write(make([]byte, 4<<20))
		errs <- err
	}()
	time.Sleep(50 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = local.Close()
		}()
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("blocked I/O returned nil error after Close")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("blocked I/O not woken by Close")
		}
	}
}

func TestMuxAcceptOnClosedMuxClosesStream(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	m := NewMux(a, "tcp", 30, false)
	_ = m.Close()

	for i := 0; i < 32; i++ {
		c := NewConn(int32(2*i+2), m)
		delivered := make(chan bool, 1)
		go func() {
			select {
			case m.newConnCh <- c:
				delivered <- true
			case <-time.After(100 * time.Millisecond):
				delivered <- false
			}
		}()
		conn, err := m.Accept()
		if err == nil || conn != nil {
			t.Fatalf("Accept() = %v, %v; want error on closed mux", conn, err)
		}
		if <-delivered && !c.IsClosed() {
			t.Fatal("stream handed to Accept on a closed mux was not closed")
		}
	}
}

func TestConnWriteOnClosedMuxReportsError(t *testing.T) {
	// the stream itself is still open, only the mux has gone away
	m := &Mux{closeChan: make(chan struct{})}
	m.writeQueue.New()
	m.newConnQueue.New()
	close(m.closeChan)
	c := NewConn(1, m)

	n, err := c.Write([]byte("data"))
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write() on closed mux error = %v, want net.ErrClosed", err)
	}
	if n != 0 {
		t.Fatalf("Write() n = %d, want 0", n)
	}
}
