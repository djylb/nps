package netaddr

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestGetCustomDNSDefaultWhenUnset(t *testing.T) {
	oldCustom := customDnsAddr
	customDnsAddr = ""
	t.Cleanup(func() {
		customDnsAddr = oldCustom
	})

	if got := customDNS(); got != "8.8.8.8:53" {
		t.Fatalf("GetCustomDNS() = %q, want %q", got, "8.8.8.8:53")
	}
}

func TestSetCustomDNSFormatsAddress(t *testing.T) {
	oldCustom := customDnsAddr
	oldResolver := net.DefaultResolver
	t.Cleanup(func() {
		customDnsAddr = oldCustom
		net.DefaultResolver = oldResolver
	})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ipv4 without port", input: "1.1.1.1", want: "1.1.1.1:53"},
		{name: "ipv4 with port", input: "1.1.1.1:5353", want: "1.1.1.1:5353"},
		{name: "ipv6 raw", input: "2001:4860:4860::8888", want: "[2001:4860:4860::8888]:53"},
		{name: "ipv6 bracketed without port", input: "[2001:4860:4860::8844]", want: "[2001:4860:4860::8844]:53"},
		{name: "ipv6 bracketed with port", input: "[2001:4860:4860::8844]:5353", want: "[2001:4860:4860::8844]:5353"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetCustomDNS(tc.input)
			if got := customDNS(); got != tc.want {
				t.Fatalf("GetCustomDNS() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetCustomDNSEmptyInputKeepsPreviousValue(t *testing.T) {
	oldCustom := customDnsAddr
	oldResolver := net.DefaultResolver
	t.Cleanup(func() {
		customDnsAddr = oldCustom
		net.DefaultResolver = oldResolver
	})

	SetCustomDNS("9.9.9.9")
	gotBefore := customDNS()
	SetCustomDNS("")
	if gotAfter := customDNS(); gotAfter != gotBefore {
		t.Fatalf("empty SetCustomDNS should keep previous value: before=%q after=%q", gotBefore, gotAfter)
	}
}

// serveTestDNS answers every query on a new loopback port with the A record ip.
func serveTestDNS(t *testing.T, ip net.IP) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			// Header and question only, without the query's EDNS record.
			end := 12
			for end < n && buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			end += 5
			resp := append([]byte{}, buf[:end]...)
			resp[2], resp[3] = 0x81, 0x80
			resp[10], resp[11] = 0, 0
			if resp[end-3] == 1 { // qtype A: one answer
				resp[7] = 1
				resp = append(resp, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 1, 0, 4)
				resp = append(resp, ip.To4()...)
			}
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestPreferredDNSFallsBackToSystem(t *testing.T) {
	system := serveTestDNS(t, net.IPv4(192, 0, 2, 2))
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()

	for _, c := range []struct {
		name, addr, want string
	}{
		{"preferred answers", serveTestDNS(t, net.IPv4(192, 0, 2, 1)), "192.0.2.1"},
		{"preferred silent", silent.LocalAddr().String(), "192.0.2.2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &preferredDNS{addr: c.addr, probeTimeout: 200 * time.Millisecond}
			r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.dial(ctx, network, system)
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			addrs, err := r.LookupHost(ctx, "npc-test.example.invalid.")
			if err != nil {
				t.Fatal(err)
			}
			if len(addrs) != 1 || addrs[0] != c.want {
				t.Fatalf("LookupHost = %v, want %s", addrs, c.want)
			}
		})
	}
}

// A lookup waits for the probe only as long as its context lasts, and other
// lookups are not held up meanwhile.
func TestPreferredDNSProbeHonoursContext(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0") // never answers
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	d := &preferredDNS{addr: pc.LocalAddr().String(), probeTimeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if d.reachable(ctx) {
		t.Fatal("silent server reachable")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("waited %v past its context", took)
	}
}

// The name of a dns_server is not looked up through itself.
func TestPreferredDNSDomainDoesNotRecurse(t *testing.T) {
	oldResolver := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = oldResolver })
	d := &preferredDNS{addr: "dns-server.invalid:53", probeTimeout: 2 * time.Second}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: d.dial}
	done := make(chan bool, 1)
	go func() { done <- d.probe() }()
	select {
	case up := <-done:
		if up {
			t.Fatal("unresolvable server reachable")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not end")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.checked.IsZero() {
		t.Fatal("the probe went through the resolver it feeds")
	}
}
