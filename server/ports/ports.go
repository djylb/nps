// Package ports checks and picks the ports that tunnels listen on, within
// the ports the server config allows.
package ports

import (
	"math/rand"
	"net"

	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/servercfg"
)

var (
	ports   []int
	portSet map[int]struct{}
)

func InitAllowPort() {
	p := servercfg.Current().Feature.AllowPorts
	ports = netaddr.GetPorts(p)
	buildAllowPortSet()
}

func buildAllowPortSet() {
	if len(ports) == 0 {
		portSet = nil
		return
	}
	set := make(map[int]struct{}, len(ports))
	for _, p := range ports {
		set[p] = struct{}{}
	}
	portSet = set
}

func TestServerPort(p int, m string) (b bool) {
	return testPortUsage(p, m, m == "udp")
}

func TestTunnelPort(t *model.Tunnel) bool {
	if t == nil {
		return false
	}
	return testPortUsage(t.Port, t.Mode, tunnelNeedsUDP(t))
}

func testPortUsage(p int, m string, needUDP bool) (b bool) {
	if m == "p2p" || m == "secret" {
		return true
	}
	if p > 65535 || p < 0 {
		return false
	}
	if len(portSet) != 0 {
		if _, ok := portSet[p]; !ok {
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

func GenerateServerPort(m string) int {
	if len(ports) > 0 {
		for _, idx := range rand.Perm(len(ports)) {
			p := ports[idx]
			if p == 0 {
				continue
			}
			if TestServerPort(p, m) {
				return p
			}
		}
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := rand.Intn(65535-1024+1) + 1024 // [1024, 65535]
			if TestServerPort(serverPort, m) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			if TestServerPort(p, m) {
				return p
			}
		}
	}
	return 0
}

func GenerateTunnelPort(t *model.Tunnel) int {
	if t == nil {
		return 0
	}
	probe := &model.Tunnel{
		Mode:        t.Mode,
		Socks5Proxy: t.Socks5Proxy,
	}
	if len(ports) > 0 {
		for _, idx := range rand.Perm(len(ports)) {
			p := ports[idx]
			if p == 0 {
				continue
			}
			probe.Port = p
			if TestTunnelPort(probe) {
				return p
			}
		}
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := rand.Intn(65535-1024+1) + 1024
			probe.Port = serverPort
			if TestTunnelPort(probe) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			probe.Port = p
			if TestTunnelPort(probe) {
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
