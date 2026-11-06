package server

import (
	"math"
	"net/netip"
	"strings"

	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/holepunch/stun"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
)

// p2pSTUNOff is the p2p_stun_servers value that hands the clients no STUN
// servers.
const p2pSTUNOff = "none"

// startP2P starts the bridge's P2P service; P2P stays off when p2p_port is
// not set or the service cannot start.
func startP2P(b *bridge.Bridge, cfg *servercfg.Snapshot) {
	port := cfg.Network.P2PPort
	if port <= 0 {
		logs.Info("P2P is off: p2p_port is not set")
		return
	}
	err := b.StartP2P(bridge.P2PConfig{
		BasePort:    port,
		IP:          cfg.Network.P2PIP,
		STUNServers: p2pSTUNServers(cfg.P2P.STUNServers),
		Timeouts: protocol.Timeouts{
			ProbeTimeoutMs:     millis(cfg.P2P.ProbeTimeoutMs),
			HandshakeTimeoutMs: millis(cfg.P2P.HandshakeTimeoutMs),
			TransportTimeoutMs: millis(cfg.P2P.TransportTimeoutMs),
		},
		PortMapping: protocol.PortMappingPolicy{
			EnableUPnP:   &cfg.P2P.EnableUPNPPortmap,
			EnablePCP:    &cfg.P2P.EnablePCPPortmap,
			EnableNATPMP: &cfg.P2P.EnableNATPMPPortmap,
			LeaseSeconds: uint32(min(max(cfg.P2P.PortmapLeaseSeconds, 0), math.MaxInt32)),
		},
	})
	if err != nil {
		logs.Error("P2P is off: the NAT observer cannot use UDP ports %d-%d: %v", port, port+2, err)
		return
	}
	if !p2pIPv6Configured(cfg.Network.P2PIP) {
		logs.Info("P2P over IPv6 is off: p2p_ip %q names IPv4 only; leave p2p_ip empty to detect both IPv4 and IPv6", cfg.Network.P2PIP)
	}
	logs.Info("P2P NAT observers started on UDP ports %d-%d, announced per client from p2p_ip %q", port, port+2, cfg.Network.P2PIP)
}

// millis converts a configured millisecond value; the library clamps it to
// its range, a negative value takes the default.
func millis(v int) uint32 {
	return uint32(min(max(v, 0), math.MaxInt32))
}

// p2pSTUNServers turns p2p_stun_servers into the STUN servers handed to the
// clients: a built-in list when empty, none for "none".
func p2pSTUNServers(v string) []string {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return stun.DefaultServers()
	case strings.EqualFold(v, p2pSTUNOff):
		return nil
	}
	return common.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(v, " ", ""), ","))
}

// p2pIPv6Configured reports whether p2p_ip lets IPv6 be announced: it is
// empty, or names :: , an IPv6 address or a host name (which may have AAAA
// records).
func p2pIPv6Configured(p2pIP string) bool {
	entries := common.HandleArrEmptyVal(strings.Split(p2pIP, ","))
	if len(entries) == 0 {
		return true
	}
	for _, v := range entries {
		ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(v), "[]"))
		if err != nil || ip.Unmap().Is6() {
			return true
		}
	}
	return false
}
