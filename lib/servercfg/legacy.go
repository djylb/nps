package servercfg

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
	{old: "log_color", note: "log_color was removed: log lines are plain logfmt or JSON (log_format)"},
	{old: "ip_limit", note: "ip_limit was removed: it made every client accept only visitors whose address " +
		"was registered (npc register). Registration-only access is now set per client, tunnel or host: " +
		"\"Registered IPs Only\" in the web UI, or registered_only=1 in the web API"},
	{old: "allow_secret_link", note: "allow_secret_link is deprecated: what a visitor of a secret tunnel may reach is the " +
		"tunnel's own destination list. While allow_secret_link=true is set, visitors of secret tunnels without a " +
		"destination list may name any destination; set a destination list on those tunnels and remove the key", ifTrue: true},
}

// legacySecretLink reads allow_secret_link of earlier versions: true lets
// visitors of secret tunnels without a destination list name any
// destination (FeatureConfig.LegacySecretLink). It goes with the next major
// version.
func legacySecretLink(r *valueReader) bool { return r.boolDefault(false, "allow_secret_link") }

// legacyGenericPort applies the rule for a config of earlier versions that
// only has the generic bridge_port: that port takes every TCP-class carrier
// of both protocols (bridge.mixGeneric) when bridge_type is of the TCP class
// (tcp, tls, ws, wss, both). With bridge_type of the UDP class (kcp, udp,
// quic) the port is a UDP port without TCP: with kcp it takes the old KCP and
// QUIC together (BridgeKCPQUIC), with udp and quic QUIC only (Snapshot.Legacy
// says so for udp, which used to take kcp). A port
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
		if cfg.Bridge.Type == "kcp" && cfg.Bridge.KCPEnable {
			// the old KCP and QUIC share the UDP port, told apart by the first packet
			n.BridgeKCPQUIC = true
			return
		}
		cfg.Bridge.ServerKCPEnabled = false
		if cfg.Bridge.Type == "udp" {
			cfg.Legacy = append(cfg.Legacy, "bridge_port with bridge_type=udp now listens QUIC only on UDP "+
				"(no TCP, no kcp): old kcp clients cannot connect; use bridge_type=kcp to serve kcp and QUIC on that port")
		}
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
