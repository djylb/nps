package ports

import (
	"net"
	"testing"

	"github.com/djylb/nps/lib/model"
)

func withPorts(t *testing.T, p []int) {
	t.Helper()
	original := ports
	originalSet := portSet
	ports = p
	buildAllowPortSet()
	t.Cleanup(func() {
		ports = original
		portSet = originalSet
	})
}

func TestTestServerPortShortCircuitAndValidation(t *testing.T) {
	withPorts(t, []int{12345})

	if !TestServerPort(-1, "p2p") {
		t.Fatal("TestServerPort() should short-circuit for p2p mode")
	}
	if !TestServerPort(70000, "secret") {
		t.Fatal("TestServerPort() should short-circuit for secret mode")
	}
	if TestServerPort(70000, "tcp") {
		t.Fatal("TestServerPort() should reject ports > 65535")
	}
	if TestServerPort(-1, "udp") {
		t.Fatal("TestServerPort() should reject ports < 0")
	}
	if TestServerPort(54321, "tcp") {
		t.Fatal("TestServerPort() should reject port not in allow list")
	}
}

func TestGenerateServerPortWithAllowList(t *testing.T) {
	withPorts(t, []int{0, 10001, 10002})
	//rand.Seed(1)

	got := GenerateServerPort("p2p")
	if got != 10001 && got != 10002 {
		t.Fatalf("GenerateServerPort()=%d, want one of configured non-zero ports", got)
	}
}

func TestGenerateServerPortWithOnlyZeroAllowList(t *testing.T) {
	withPorts(t, []int{0, 0})
	if got := GenerateServerPort("p2p"); got != 0 {
		t.Fatalf("GenerateServerPort()=%d, want 0 when allow list has no usable ports", got)
	}
}

func TestGenerateServerPortWithoutAllowListUsesDynamicRange(t *testing.T) {
	withPorts(t, nil)
	//rand.Seed(1)

	got := GenerateServerPort("p2p")
	if got < 1024 || got > 65535 {
		t.Fatalf("GenerateServerPort()=%d, want in [1024, 65535]", got)
	}
}

func TestTestTunnelPortChecksUDPForSocks5(t *testing.T) {
	withPorts(t, nil)

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() { _ = udpListener.Close() }()

	port := udpListener.LocalAddr().(*net.UDPAddr).Port
	socksTunnel := &model.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: true}
	if TestTunnelPort(socksTunnel) {
		t.Fatalf("TestTunnelPort() should reject socks5 tunnel when UDP port %d is occupied", port)
	}

	httpTunnel := &model.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: false}
	if !TestTunnelPort(httpTunnel) {
		t.Fatalf("TestTunnelPort() should allow HTTP-only mixProxy when only UDP port %d is occupied", port)
	}
}

func TestGenerateTunnelPortUsesTunnelPolicy(t *testing.T) {
	withPorts(t, []int{0, 10001, 10002})

	got := GenerateTunnelPort(&model.Tunnel{Mode: "mixProxy", Socks5Proxy: true})
	if got != 10001 && got != 10002 {
		t.Fatalf("GenerateTunnelPort()=%d, want one of configured non-zero ports", got)
	}
}
