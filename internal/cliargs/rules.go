package cliargs

import "strconv"

// NPC are the npc flags of earlier versions (0.26 to 0.34) that npc no
// longer defines.
var NPC = []Rule{
	// -tls_enable made the connection type tls, whatever -type said
	// (0.34 cmd/npc: if *tlsEnable { *connType = "tls" }).
	{Name: "tls_enable", Bool: true, Translate: func(value string, hasValue bool) ([]string, string) {
		on := true
		if hasValue {
			var err error
			if on, err = strconv.ParseBool(value); err != nil {
				return nil, "not a boolean, ignored"
			}
		}
		if !on {
			return nil, "old flag, ignored; it has no effect when false"
		}
		return []string{"--type=tls"}, "old spelling of -type=tls, used as -type=tls"
	}},
	ignore("proto_version", false, "ignored: npc only speaks the latest protocol; if the server is older, update the server first"),
	ignore("stun_addr", false, "ignored: it no longer has an effect; STUN servers are now set on the server (p2p_stun_servers in nps.conf)"),
	ignore("p2p_type", false, "ignored: it no longer has an effect; P2P picks its transport itself"),
	ignore("log_daily", false, "ignored: it no longer has an effect; logs rotate by -log_max_size and -log_max_days"),
}

// NPS are the nps flags of earlier versions that nps no longer defines.
var NPS []Rule

// ignore is the rule of a flag dropped with warning.
func ignore(name string, isBool bool, warning string) Rule {
	return Rule{Name: name, Bool: isBool, Translate: func(string, bool) ([]string, string) {
		return nil, warning
	}}
}
