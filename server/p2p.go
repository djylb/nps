package server

import (
	"context"
	"math"
	"net/netip"
	"strings"

	"github.com/djylb/holepunch/protocol"
	"github.com/djylb/holepunch/stun"
	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/slogx"
)

// p2pSTUNOff is the p2p_stun_servers value that hands the clients no STUN
// servers.
const p2pSTUNOff = "none"

// startP2P starts the bridge's P2P service; P2P stays off when p2p_port is
// not set or the service cannot start.
func startP2P(b *bridge.Bridge, cfg *servercfg.Snapshot) {
	port := cfg.Network.P2PPort
	if port <= 0 {
		log.Info("p2p off", "reason", "no_port")
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
		slogx.Emit(context.Background(), log, &logevent.ListenFailed, slogx.KeyMsg, "p2p off: nat observer cannot use udp ports", "route", "p2p", "port", port, "count", 3, "reason", logevent.NetReason(err), "err", err)
		return
	}
	if !p2pIPv6Configured(cfg.Network.P2PIP) {
		log.Warn("p2p over ipv6 off", "detail", "p2p_ip names ipv4 only; leave it empty to detect both")
	}
	log.Info("p2p nat observers started", "route", "p2p", "port", port, "count", 3)
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
	return textutil.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(v, " ", ""), ","))
}

// p2pIPv6Configured reports whether p2p_ip lets IPv6 be announced: it is
// empty, or names :: , an IPv6 address or a host name (which may have AAAA
// records).
func p2pIPv6Configured(p2pIP string) bool {
	entries := textutil.HandleArrEmptyVal(strings.Split(p2pIP, ","))
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
