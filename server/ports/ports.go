// Package ports checks and picks the ports that tunnels listen on, within
// the ports the server config allows.
package ports

import (
	"math/rand"
	"net"

	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
)

// Allowed is the set of ports tunnels may listen on. The zero value allows
// every port.
type Allowed struct {
	ports []int
	set   map[int]struct{}
}

// ParseAllowed returns the ports of spec, the allow_ports setting: ports and
// ranges such as "8000-9000,9999". An empty spec allows every port.
func ParseAllowed(spec string) Allowed {
	return newAllowed(netaddr.GetPorts(spec))
}

func newAllowed(ports []int) Allowed {
	if len(ports) == 0 {
		return Allowed{}
	}
	set := make(map[int]struct{}, len(ports))
	for _, p := range ports {
		set[p] = struct{}{}
	}
	return Allowed{ports: ports, set: set}
}

// TestServerPort reports whether a listener of mode m can open on port p.
func (a Allowed) TestServerPort(p int, m string) (b bool) {
	return a.testPortUsage(p, m, m == "udp")
}

// TestTunnelPort reports whether the listeners of t can open on its port.
func (a Allowed) TestTunnelPort(t *model.Tunnel) bool {
	if t == nil {
		return false
	}
	return a.testPortUsage(t.Port, t.Mode, tunnelNeedsUDP(t))
}

func (a Allowed) testPortUsage(p int, m string, needUDP bool) (b bool) {
	if m == "p2p" || m == "secret" {
		return true
	}
	if p > 65535 || p < 0 {
		return false
	}
	if len(a.set) != 0 {
		if _, ok := a.set[p]; !ok {
			return false
		}
	}
	needTCP := m != "udp"
	if needTCP && !canListenTCP(p) {
		return false
	}
	if needUDP && !canListenUDP(p) {
		return false
	}
	return true
}

func tunnelNeedsUDP(t *model.Tunnel) bool {
	if t == nil {
		return false
	}
	switch t.Mode {
	case "udp", "socks5":
		return true
	case "mixProxy":
		return t.Socks5Proxy
	default:
		return false
	}
}

// GenerateServerPort picks a free allowed port for a listener of mode m, or
// 0 when there is none.
func (a Allowed) GenerateServerPort(m string) int {
	if len(a.ports) > 0 {
		for _, idx := range rand.Perm(len(a.ports)) {
			p := a.ports[idx]
			if p == 0 {
				continue
			}
			if a.TestServerPort(p, m) {
				return p
			}
		}
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := rand.Intn(65535-1024+1) + 1024 // [1024, 65535]
			if a.TestServerPort(serverPort, m) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			if a.TestServerPort(p, m) {
				return p
			}
		}
	}
	return 0
}

// GenerateTunnelPort picks a free allowed port for the listeners of t, or 0
// when there is none.
func (a Allowed) GenerateTunnelPort(t *model.Tunnel) int {
	if t == nil {
		return 0
	}
	probe := &model.Tunnel{
		Mode:        t.Mode,
		Socks5Proxy: t.Socks5Proxy,
	}
	if len(a.ports) > 0 {
		for _, idx := range rand.Perm(len(a.ports)) {
			p := a.ports[idx]
			if p == 0 {
				continue
			}
			probe.Port = p
			if a.TestTunnelPort(probe) {
				return p
			}
		}
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := rand.Intn(65535-1024+1) + 1024
			probe.Port = serverPort
			if a.TestTunnelPort(probe) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			probe.Port = p
			if a.TestTunnelPort(probe) {
				return p
			}
		}
	}
	return 0
}

// canListenTCP reports whether a TCP listener can open on port.
func canListenTCP(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// canListenUDP reports whether a UDP listener can open on port.
func canListenUDP(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}
