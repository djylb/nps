package tool

import (
	"net"
	"testing"

	"github.com/djylb/nps/lib/file"
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

func resetStatusState() {
	ssMu.Lock()
	defer ssMu.Unlock()
	for i := range statBuf {
		statBuf[i] = nil
	}
	statIdx = 0
	statFilled = false
}

func fillStatusEntries(n int) {
	for i := 0; i < n; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = n
	if n >= statusCap {
		statIdx = n % statusCap
		statFilled = true
	}
}

func TestStatusCountAndSnapshotWithoutWrap(t *testing.T) {
	resetStatusState()
	fillStatusEntries(3)

	if got := statusCount(); got != 3 {
		t.Fatalf("statusCount()=%d, want 3", got)
	}

	snapshot := StatusSnapshot()
	if len(snapshot) != 3 {
		t.Fatalf("len(StatusSnapshot())=%d, want 3", len(snapshot))
	}

	for i := 0; i < 3; i++ {
		if snapshot[i]["id"].(int) != i {
			t.Fatalf("snapshot[%d].id=%v, want %d", i, snapshot[i]["id"], i)
		}
	}
}

func TestStatusSnapshotWithWrap(t *testing.T) {
	resetStatusState()
	ssMu.Lock()
	for i := 0; i < statusCap; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	snapshot := StatusSnapshot()
	if len(snapshot) != statusCap {
		t.Fatalf("len(StatusSnapshot())=%d, want %d", len(snapshot), statusCap)
	}
	if snapshot[0]["id"].(int) != 100 {
		t.Fatalf("snapshot[0].id=%v, want 100", snapshot[0]["id"])
	}
	if snapshot[len(snapshot)-1]["id"].(int) != 99 {
		t.Fatalf("snapshot[last].id=%v, want 99", snapshot[len(snapshot)-1]["id"])
	}
}

func TestChartDecilesEdgeCasesAndSampling(t *testing.T) {
	resetStatusState()
	if got := ChartDeciles(); got != nil {
		t.Fatalf("ChartDeciles()=%v, want nil for empty state", got)
	}

	resetStatusState()
	fillStatusEntries(5)
	small := ChartDeciles()
	if len(small) != 5 {
		t.Fatalf("len(ChartDeciles())=%d, want 5", len(small))
	}
	for i := 0; i < 5; i++ {
		if small[i]["id"].(int) != i {
			t.Fatalf("small[%d].id=%v, want %d", i, small[i]["id"], i)
		}
	}

	resetStatusState()
	ssMu.Lock()
	for i := 0; i < statusCap; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	deciles := ChartDeciles()
	if len(deciles) != 10 {
		t.Fatalf("len(ChartDeciles())=%d, want 10", len(deciles))
	}

	for i := 0; i < 10; i++ {
		pos := (i * (statusCap - 1)) / 9
		expected := (100 + pos) % statusCap
		if deciles[i]["id"].(int) != expected {
			t.Fatalf("deciles[%d].id=%v, want %d", i, deciles[i]["id"], expected)
		}
	}
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
	socksTunnel := &file.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: true}
	if TestTunnelPort(socksTunnel) {
		t.Fatalf("TestTunnelPort() should reject socks5 tunnel when UDP port %d is occupied", port)
	}

	httpTunnel := &file.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: false}
	if !TestTunnelPort(httpTunnel) {
		t.Fatalf("TestTunnelPort() should allow HTTP-only mixProxy when only UDP port %d is occupied", port)
	}
}

func TestGenerateTunnelPortUsesTunnelPolicy(t *testing.T) {
	withPorts(t, []int{0, 10001, 10002})

	got := GenerateTunnelPort(&file.Tunnel{Mode: "mixProxy", Socks5Proxy: true})
	if got != 10001 && got != 10002 {
		t.Fatalf("GenerateTunnelPort()=%d, want one of configured non-zero ports", got)
	}
}
