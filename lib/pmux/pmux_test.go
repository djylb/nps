package pmux

import (
	"net"
	"testing"

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
	pMux := NewPortMux(port, "Ds", "Cs")
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
	pMux := &PortMux{}
	if err := pMux.Close(); err != nil {
		t.Fatalf("close without listeners failed: %v", err)
	}
}

func TestPortMux_ProcessNilConn(t *testing.T) {
	pMux := &PortMux{}
	pMux.process(nil)
}
