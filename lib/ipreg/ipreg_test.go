package ipreg

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestRegistered(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	ip := netip.MustParseAddr("192.0.2.1")
	Register(3, ip, time.Hour)
	if !Registered(3, ip) || Registered(4, ip) {
		t.Fatal("registration not bound to its client")
	}
	if !RegisteredString(3, "192.0.2.1:5678") || !RegisteredString(3, "[::ffff:192.0.2.1]:80") {
		t.Fatal("address forms not matched")
	}
	if !RegisteredAddr(3, &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1}) || RegisteredAddr(3, (*net.TCPAddr)(nil)) {
		t.Fatal("net.Addr forms")
	}

	other := netip.MustParseAddr("2001:db8::1")
	Register(AnyClient, other, time.Hour)
	if !Registered(7, other) || !Registered(3, other) {
		t.Fatal("registration for every client not applied")
	}

	// a shorter registration does not cut a longer one
	until := Register(3, ip, time.Minute)
	if time.Until(until) < 50*time.Minute {
		t.Fatal("shorter registration shortened a longer one")
	}

	Register(5, ip, -time.Second)
	if Registered(5, ip) {
		t.Fatal("expired registration still valid")
	}
	if n := Count(); n != 2 {
		t.Fatalf("Count = %d, want 2", n)
	}
}
