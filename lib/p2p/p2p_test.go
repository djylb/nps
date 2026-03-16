package p2p

import (
	"errors"
	"net"
	"testing"
)

func TestGetNextAddr(t *testing.T) {
	got, err := getNextAddr("127.0.0.1:2000", 5)
	if err != nil {
		t.Fatalf("getNextAddr returned error: %v", err)
	}
	if got != "127.0.0.1:2005" {
		t.Fatalf("getNextAddr = %q, want %q", got, "127.0.0.1:2005")
	}

	if _, err := getNextAddr("127.0.0.1", 1); err == nil {
		t.Fatal("expected invalid address to return error")
	}
}

func TestGetAddrInterval(t *testing.T) {
	tests := []struct {
		name    string
		a1      string
		a2      string
		a3      string
		want    int
		wantErr bool
	}{
		{name: "positive interval", a1: "1.1.1.1:1000", a2: "1.1.1.1:1003", a3: "1.1.1.1:1006", want: 3},
		{name: "negative interval", a1: "1.1.1.1:1006", a2: "1.1.1.1:1003", a3: "1.1.1.1:1000", want: -3},
		{name: "invalid input", a1: "bad", a2: "1.1.1.1:1003", a3: "1.1.1.1:1000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getAddrInterval(tt.a1, tt.a2, tt.a3)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("getAddrInterval returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("getAddrInterval = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetRandomUniquePorts(t *testing.T) {
	ports := getRandomUniquePorts(20, 10000, 10030)
	if len(ports) != 20 {
		t.Fatalf("len(ports) = %d, want 20", len(ports))
	}
	seen := make(map[int]struct{}, len(ports))
	for _, p := range ports {
		if p < 10000 || p > 10030 {
			t.Fatalf("port %d is out of range", p)
		}
		if _, ok := seen[p]; ok {
			t.Fatalf("duplicate port %d", p)
		}
		seen[p] = struct{}{}
	}

	all := getRandomUniquePorts(100, 2000, 2002)
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}

	nilPorts := getRandomUniquePorts(1, 5, 4)
	if len(nilPorts) != 1 {
		t.Fatalf("min/max swap failed, got len = %d", len(nilPorts))
	}
}

func TestShouldRunFallbackRandomScan(t *testing.T) {
	tests := []struct {
		name                      string
		aggressive, forceHard, pr bool
		want                      bool
	}{
		{name: "all false", aggressive: false, forceHard: false, pr: false, want: false},
		{name: "aggressive", aggressive: true, forceHard: false, pr: false, want: true},
		{name: "force-hard", aggressive: false, forceHard: true, pr: false, want: true},
		{name: "port-restricted", aggressive: false, forceHard: false, pr: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRunFallbackRandomScan(tt.aggressive, tt.forceHard, tt.pr); got != tt.want {
				t.Fatalf("shouldRunFallbackRandomScan(%v,%v,%v)=%v, want %v", tt.aggressive, tt.forceHard, tt.pr, got, tt.want)
			}
		})
	}
}

func TestPickPrimaryPunchTarget(t *testing.T) {
	exact := []string{"1.1.1.1:1000", "1.1.1.1:1001"}
	pred := []string{"1.1.1.1:1003", "1.1.1.1:1002"}

	if got := pickPrimaryPunchTarget(exact, pred, true); got != "1.1.1.1:1003" {
		t.Fatalf("aggressive primary = %q, want %q", got, "1.1.1.1:1003")
	}
	if got := pickPrimaryPunchTarget(exact, pred, false); got != "1.1.1.1:1000" {
		t.Fatalf("conservative primary = %q, want %q", got, "1.1.1.1:1000")
	}
	if got := pickPrimaryPunchTarget(nil, pred, false); got != "1.1.1.1:1003" {
		t.Fatalf("fallback prediction primary = %q, want %q", got, "1.1.1.1:1003")
	}
	if got := pickPrimaryPunchTarget(nil, nil, true); got != "" {
		t.Fatalf("empty primary = %q, want empty", got)
	}
}

func TestBuildSmallContiguousPorts(t *testing.T) {
	ports := buildSmallContiguousPorts(100, 2)
	want := []int{100, 101, 99, 102, 98}
	if len(ports) != len(want) {
		t.Fatalf("len(ports)=%d, want %d (%#v)", len(ports), len(want), ports)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("ports[%d]=%d, want %d (%#v)", i, ports[i], want[i], ports)
		}
	}

	edge := buildSmallContiguousPorts(1, 2)
	if len(edge) == 0 || edge[0] != 1 {
		t.Fatalf("unexpected edge result: %#v", edge)
	}
	for _, p := range edge {
		if p < 1 || p > 65535 {
			t.Fatalf("out-of-range port in edge result: %#v", edge)
		}
	}

	if got := buildSmallContiguousPorts(0, 3); len(got) != 0 {
		t.Fatalf("invalid base should return empty, got %#v", got)
	}
}

func TestBuildPredictedPeerAddrs(t *testing.T) {
	pred := buildPredictedPeerAddrs("1.1.1.1:1000", "1.1.1.1:1002", "2.2.2.2:1004", 2)
	if len(pred) == 0 {
		t.Fatal("expected predicted addrs")
	}
	want := map[string]bool{
		"2.2.2.2:1006": true,
		"2.2.2.2:1002": true,
		"1.1.1.1:1004": true,
		"1.1.1.1:1000": true,
		"1.1.1.1:1002": true,
		"1.1.1.1:998":  true,
	}
	for _, a := range pred {
		delete(want, a)
	}
	if len(want) != 0 {
		t.Fatalf("missing predicted addrs: %#v", want)
	}

	if got := buildPredictedPeerAddrs("1.1.1.1:1000", "", "", 0); len(got) != 0 {
		t.Fatalf("interval 0 should return empty, got %#v", got)
	}
}

func TestP2PHelpers(t *testing.T) {
	if got := natHintByInterval(0, false); got != "unknown" {
		t.Fatalf("natHintByInterval unknown = %q", got)
	}
	if got := natHintByInterval(0, true); got != "cone-ish" {
		t.Fatalf("natHintByInterval cone-ish = %q", got)
	}
	if got := natHintByInterval(2, true); got != "symmetric-ish" {
		t.Fatalf("natHintByInterval symmetric-ish = %q", got)
	}

	uniq := uniqAddrStrs(" 1.1.1.1:80 ", "", "1.1.1.1:80", "2.2.2.2:90")
	if len(uniq) != 2 || uniq[0] != "1.1.1.1:80" || uniq[1] != "2.2.2.2:90" {
		t.Fatalf("uniqAddrStrs got %#v", uniq)
	}

	if resolveUDPAddr("") != nil {
		t.Fatal("resolveUDPAddr empty input should return nil")
	}
	if resolveUDPAddr("not-an-addr") != nil {
		t.Fatal("resolveUDPAddr invalid input should return nil")
	}
	ua := resolveUDPAddr("127.0.0.1:12345")
	if ua == nil {
		t.Fatal("resolveUDPAddr valid input should return *net.UDPAddr")
	}
	if _, ok := interface{}(ua).(*net.UDPAddr); !ok {
		t.Fatalf("resolveUDPAddr returned unexpected type %T", ua)
	}

	if got := hostOnly("127.0.0.1:8080"); got != "127.0.0.1" {
		t.Fatalf("hostOnly host:port = %q", got)
	}
	if got := hostOnly("example.com"); got != "example.com" {
		t.Fatalf("hostOnly hostname only = %q", got)
	}
	if got := hostOnly(""); got != "" {
		t.Fatalf("hostOnly empty = %q", got)
	}

	if isRegularStep(0, true) {
		t.Fatal("interval 0 should not be regular")
	}
	if isRegularStep(6, true) {
		t.Fatal("interval 6 should not be regular")
	}
	if !isRegularStep(-3, true) {
		t.Fatal("interval -3 should be regular")
	}
	if isRegularStep(3, false) {
		t.Fatal("has=false should not be regular")
	}
}

func TestFillTripletByPortDiff(t *testing.T) {
	tests := []struct {
		name       string
		a1, a2, a3 string
		want1      string
		want2      string
		want3      string
	}{
		{
			name:  "keep complete triplet",
			a1:    "1.1.1.1:1000",
			a2:    "1.1.1.1:1003",
			a3:    "1.1.1.1:1006",
			want1: "1.1.1.1:1000",
			want2: "1.1.1.1:1003",
			want3: "1.1.1.1:1006",
		},
		{
			name:  "fill missing first",
			a2:    "1.1.1.1:2003",
			a3:    "1.1.1.1:2006",
			want1: "1.1.1.1:2000",
			want2: "1.1.1.1:2003",
			want3: "1.1.1.1:2006",
		},
		{
			name:  "fill missing middle",
			a1:    "1.1.1.1:2000",
			a3:    "1.1.1.1:2006",
			want1: "1.1.1.1:2000",
			want2: "1.1.1.1:2003",
			want3: "1.1.1.1:2006",
		},
		{
			name:  "fill missing last",
			a1:    "1.1.1.1:2000",
			a2:    "1.1.1.1:2003",
			want1: "1.1.1.1:2000",
			want2: "1.1.1.1:2003",
			want3: "1.1.1.1:2006",
		},
		{
			name:  "zero diff uses existing endpoint",
			a2:    "1.1.1.1:3000",
			a3:    "1.1.1.1:3000",
			want1: "1.1.1.1:3000",
			want2: "1.1.1.1:3000",
			want3: "1.1.1.1:3000",
		},
		{
			name:  "invalid input keeps original",
			a2:    "bad",
			a3:    "1.1.1.1:3000",
			want1: "",
			want2: "bad",
			want3: "1.1.1.1:3000",
		},
		{
			name:  "clamp low port",
			a2:    "1.1.1.1:2",
			a3:    "1.1.1.1:1",
			want1: "1.1.1.1:3",
			want2: "1.1.1.1:2",
			want3: "1.1.1.1:1",
		},
		{
			name:  "clamp high port",
			a1:    "1.1.1.1:65534",
			a2:    "1.1.1.1:65535",
			want1: "1.1.1.1:65534",
			want2: "1.1.1.1:65535",
			want3: "1.1.1.1:65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1, got2, got3 := fillTripletByPortDiff(tt.a1, tt.a2, tt.a3)
			if got1 != tt.want1 || got2 != tt.want2 || got3 != tt.want3 {
				t.Fatalf("fillTripletByPortDiff(%q,%q,%q)=(%q,%q,%q), want (%q,%q,%q)",
					tt.a1, tt.a2, tt.a3, got1, got2, got3, tt.want1, tt.want2, tt.want3)
			}
		})
	}
}

func TestIsIgnorableUDPIcmpError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "windows 10054", err: errors.New("wsarecvfrom: An existing connection was forcibly closed by the remote host. 10054"), want: true},
		{name: "linux connection refused", err: errors.New("read udp 1.1.1.1:1->2.2.2.2:2: connection refused"), want: true},
		{name: "connection reset by peer", err: errors.New("connection reset by peer"), want: true},
		{name: "other error", err: errors.New("use of closed network connection"), want: false},
		{name: "port contains 10054 only", err: errors.New("read udp 127.0.0.1:10054: use of closed network connection"), want: false},
		{name: "winsock symbolic", err: errors.New("WSARecvFrom failed with WSAECONNRESET"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIgnorableUDPIcmpError(tt.err); got != tt.want {
				t.Fatalf("isIgnorableUDPIcmpError(%v)=%v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsP2PNATProbePacket(t *testing.T) {
	if !isP2PNATProbePacket([]byte("p2px*#*p2pv*#*1.1.1.1:1*#*")) {
		t.Fatal("expected nat-probe packet to be recognized")
	}
	if isP2PNATProbePacket([]byte("p2pc")) {
		t.Fatal("non-probe packet should not be recognized as nat-probe")
	}
}
