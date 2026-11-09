package servercfg

import (
	"slices"
	"strings"
)

// This file is the one place nps.conf keys of earlier versions are handled,
// so that a config written for an earlier nps keeps working, or says what to
// do instead, after an update.

// legacyKey is a key earlier versions read. A renamed key has current set
// and is read where current is unset; a removed key has only a note.
type legacyKey struct {
	old     string
	current string
	note    string
	// ifTrue: a boolean key that only matters, and is only warned about,
	// when true.
	ifTrue bool
}

var legacyKeys = []legacyKey{
	{old: "tls_bridge_port", current: "bridge_tls_port"},
	{old: "new_protocol", note: "new_protocol was removed: the core protocol is always on"},
	{old: "log_color", note: "log_color was removed: log lines are plain logfmt or JSON (log_format)"},
	{old: "ip_limit", note: "ip_limit was removed: it made every client accept only visitors whose address " +
		"was registered (npc register). Registration-only access is now set per client, tunnel or host: " +
		"\"Registered IPs Only\" in the web UI, or registered_only=1 in the web API"},
	{old: "allow_secret_link", note: "allow_secret_link was removed: visitors of secret tunnels may name any destination unless the tunnel's destination list says otherwise"},
}

// legacyGenericPort applies the rule for a config of earlier versions that
// only has the generic bridge_port: that port takes every TCP-class carrier
// of both protocols (bridge.mixGeneric) when bridge_type is of the TCP class
// (tcp, tls, ws, wss, both). With bridge_type of the UDP class (kcp, udp,
// quic) the port is a UDP port without TCP that takes QUIC, and with kcp and
// udp the old KCP too (NetworkConfig.BridgeKCPQUIC). A port
// shared with the web manager or the domain proxy stays with the port
// multiplexer. It goes with the next major version.
func legacyGenericPort(r *valueReader, cfg *Snapshot) {
	n := &cfg.Network
	if n.BridgePort == 0 || n.HTTPProxyPort == n.BridgePort || n.HTTPSProxyPort == n.BridgePort || n.WebPort == n.BridgePort {
		return
	}
	for _, key := range []string{"bridge_tcp_port", "bridge_tls_port", "tls_bridge_port", "bridge_ws_port", "bridge_wss_port"} {
		if _, err := lookupValue(r.values, key); err == nil {
			return
		}
	}
	if _, err := lookupValue(r.values, "bridge_port"); err != nil {
		return
	}
	switch cfg.Bridge.Type {
	case "kcp", "udp", "quic":
		// a UDP port written by itself keeps its meaning
		for _, key := range []string{"bridge_kcp_port", "bridge_quic_port"} {
			if _, err := lookupValue(r.values, key); err == nil {
				return
			}
		}
		n.BridgeQUICPort = n.BridgePort
		cfg.Bridge.ServerQUICEnabled = cfg.Bridge.QUICEnable
		cfg.Bridge.ServerTCPEnabled = false
		return
	}
	n.BridgeGeneric = true
	cfg.Bridge.ServerTCPEnabled = cfg.Bridge.TCPEnable
}

// names returns key followed by the old names it was read under, so that a
// reader takes key first and an old name where key is unset.
func names(key string) []string {
	out := []string{key}
	for _, l := range legacyKeys {
		if l.current == key {
			out = append(out, l.old)
		}
	}
	return out
}

// legacyWarnings returns a warning for each legacy key set in values.
func legacyWarnings(values map[string]any) []string {
	var out []string
	for _, l := range legacyKeys {
		v, err := lookupValue(values, l.old)
		if err != nil {
			continue
		}
		if b, err := toBool(v); l.ifTrue && (err != nil || !b) {
			continue
		}
		if l.current != "" {
			out = append(out, l.old+" is the old name of "+l.current+"; please rename it")
			continue
		}
		out = append(out, l.note)
	}
	return out
}

// legacyQUICALPN keeps a config of earlier versions starting when its
// quic_alpn shares a name with the core protocol's default
// new_protocol_quic_alpn (h3): the name goes to the core protocol and is
// dropped from the old list, with a warning. An empty old list falls back to
// the old default. A new_protocol_quic_alpn written in the config is the
// administrator's choice and an overlap with it stays an error
// (NetworkConfig.CheckQUICALPN). It goes with the next major version.
func legacyQUICALPN(r *valueReader, cfg *Snapshot) {
	n := &cfg.Network
	if r.stringValue("new_protocol_quic_alpn") != "" {
		return
	}
	var dropped []string
	n.QUICALPNList = slices.DeleteFunc(n.QUICALPNList, func(a string) bool {
		if slices.Contains(n.NewProtocolQUICALPN, a) {
			dropped = append(dropped, a)
			return true
		}
		return false
	})
	if len(dropped) == 0 {
		return
	}
	msg := "quic_alpn names used by the core protocol (new_protocol_quic_alpn default) were dropped from it: "
	for i, a := range dropped {
		if i > 0 {
			msg += ","
		}
		msg += a
	}
	if len(n.QUICALPNList) == 0 {
		n.QUICALPNList = []string{"nps"}
		msg += "; quic_alpn is now nps, so old clients need that ALPN"
	}
	n.QUICALPN = strings.Join(n.QUICALPNList, ",")
	cfg.Legacy = append(cfg.Legacy, msg)
}
