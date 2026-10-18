// Package ipreg keeps the visitor addresses registered for clients whose
// entries only allow registered addresses. Registrations expire and live in
// memory only.
package ipreg

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// AnyClient registers an address for every client, as an administrator's
// login does.
const AnyClient = 0

var (
	mu  sync.Mutex
	reg = map[int]map[netip.Addr]time.Time{}
)

// Register registers ip for client until ttl from now; a registration that
// already lasts longer is kept.
func Register(client int, ip netip.Addr, ttl time.Duration) time.Time {
	ip = ip.Unmap().WithZone("")
	until := time.Now().Add(ttl)
	mu.Lock()
	defer mu.Unlock()
	m := reg[client]
	if m == nil {
		m = map[netip.Addr]time.Time{}
		reg[client] = m
	}
	if cur, ok := m[ip]; ok && cur.After(until) {
		return cur
	}
	m[ip] = until
	return until
}

// Registered reports whether ip is registered for client, by a registration
// for that client or for every client.
func Registered(client int, ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("")
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	return valid(reg[client], ip, now) || valid(reg[AnyClient], ip, now)
}

func valid(m map[netip.Addr]time.Time, ip netip.Addr, now time.Time) bool {
	until, ok := m[ip]
	if !ok {
		return false
	}
	if !until.After(now) {
		delete(m, ip)
		return false
	}
	return true
}

// RegisteredAddr is Registered for a network address.
func RegisteredAddr(client int, addr net.Addr) bool {
	ip, ok := addrIP(addr)
	return ok && Registered(client, ip)
}

// RegisteredString is Registered for an address given as text, an IP or
// host:port.
func RegisteredString(client int, addr string) bool {
	ip, ok := ParseIP(addr)
	return ok && Registered(client, ip)
}

// Count returns the number of registrations in effect.
func Count() int {
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for client, m := range reg {
		for ip, until := range m {
			if until.After(now) {
				n++
			} else {
				delete(m, ip)
			}
		}
		if len(m) == 0 {
			delete(reg, client)
		}
	}
	return n
}

// Reset removes every registration.
func Reset() {
	mu.Lock()
	reg = map[int]map[netip.Addr]time.Time{}
	mu.Unlock()
}

func addrIP(addr net.Addr) (netip.Addr, bool) {
	switch a := addr.(type) {
	case nil:
		return netip.Addr{}, false
	case *net.TCPAddr:
		if a == nil {
			return netip.Addr{}, false
		}
		return a.AddrPort().Addr(), true
	case *net.UDPAddr:
		if a == nil {
			return netip.Addr{}, false
		}
		return a.AddrPort().Addr(), true
	}
	return ParseIP(addr.String())
}

// ParseIP reads an IP address given alone or as host:port.
func ParseIP(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip, true
	}
	return netip.Addr{}, false
}
