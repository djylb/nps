package pmux

import (
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/logs"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port failed: %v", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type: %T", l.Addr())
	}
	return addr.Port
}

func TestPortMux_ListenersAndClose(t *testing.T) {
	logs.Init("stdout", "trace", "", 0, 0, 0, false, true)

	port := getFreePort(t)
	pMux, err := NewPortMux(port, "Ds", "Cs")
	if err != nil {
		t.Fatal(err)
	}
	if pMux.Listener == nil {
		t.Fatal("port mux listener not initialized")
	}

	if pMux.GetClientListener() == nil {
		t.Fatal("client listener is nil")
	}
	if pMux.GetClientTlsListener() == nil {
		t.Fatal("client tls listener is nil")
	}
	if pMux.GetHttpListener() == nil {
		t.Fatal("http listener is nil")
	}
	if pMux.GetHttpsListener() == nil {
		t.Fatal("https listener is nil")
	}
	if pMux.GetManagerListener() == nil {
		t.Fatal("manager listener is nil")
	}

	if err := pMux.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestPortMux_CloseWithoutListeners(t *testing.T) {
	pMux := newPortMux(0, "", "")
	if err := pMux.Close(); err != nil {
		t.Fatalf("close without listeners failed: %v", err)
	}
	if err := pMux.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second close err = %v, want net.ErrClosed", err)
	}
}

func TestPortMux_ProcessNilConn(t *testing.T) {
	pMux := &PortMux{}
	pMux.process(nil)
}

func TestPortMux_RoutesClientAndClosesListeners(t *testing.T) {
	port := getFreePort(t)
	pMux, err := NewPortMux(port, "Ds", "Cs")
	if err != nil {
		t.Fatal(err)
	}
	clientL := pMux.GetClientListener()
	httpL := pMux.GetHttpListener()

	const conns = 20
	for i := 0; i < conns; i++ {
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		if _, err := c.Write([]byte("TST payload")); err != nil {
			t.Fatalf("write: %v", err)
		}
		time.Sleep(time.Millisecond) // delivery must wait for a late Accept
		got, err := clientL.Accept()
		if err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
		buf := make([]byte, len("TST payload"))
		if _, err := io.ReadFull(got, buf); err != nil || string(buf) != "TST payload" {
			t.Fatalf("read %q, %v", buf, err)
		}
		_ = got.Close()
		_ = c.Close()
	}

	// Replacing a listener closes the previous one.
	newClientL := pMux.GetClientListener()
	if _, err := clientL.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("replaced listener accept err = %v, want net.ErrClosed", err)
	}

	errCh := make(chan error, 2)
	for _, l := range []net.Listener{newClientL, httpL} {
		go func() {
			_, err := l.Accept()
			errCh <- err
		}()
	}
	_ = pMux.Close()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatalf("accept after mux close err = %v, want net.ErrClosed", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("accept not woken by mux close")
		}
	}
}

func TestPortMux_DropsUnroutableConn(t *testing.T) {
	port := getFreePort(t)
	pMux, err := NewPortMux(port, "Ds", "Cs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pMux.Close() }()
	_ = pMux.GetHttpListener()

	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("TST")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read err = %v, want EOF from closed conn", err)
	}
}

func TestPortMux_HttpRoutingByHostAfterHttpListenerClosed(t *testing.T) {
	port := getFreePort(t)
	pMux, err := NewPortMux(port, "admin.test", "bridge.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pMux.Close() }()
	managerL := pMux.GetManagerListener()
	_ = pMux.GetHttpListener().Close()

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := managerL.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	send := func(host string) net.Conn {
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		return c
	}

	// A host other than the manager host must not reach the manager.
	other := send("other.test")
	defer func() { _ = other.Close() }()
	_ = other.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := other.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read err = %v, want EOF from closed conn", err)
	}

	mgr := send("admin.test")
	defer func() { _ = mgr.Close() }()
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("manager host request not delivered to manager")
	}
	select {
	case c := <-accepted:
		_ = c.Close()
		t.Fatal("non-manager host request delivered to manager")
	default:
	}
}
