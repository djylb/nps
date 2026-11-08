package cliargs

import (
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/config"
)

// The translations of earlier flags in this file are removed in the next
// major version.

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
	ignore("new_protocol", true, "ignored: the core protocol is the only one npc speaks"),
	ignore("proto_version", false, "ignored: npc only speaks the latest protocol; if the server is older, update the server first"),
	ignore("stun_addr", false, "ignored: it no longer has an effect; STUN servers are now set on the server (p2p_stun_servers in nps.conf)"),
	ignore("p2p_type", false, "ignored: it no longer has an effect; P2P picks its transport itself"),
	ignore("log_color", false, "ignored: log lines are plain logfmt or JSON (-log_format)"),
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

// NPCLocal translates the point-to-point access flags of earlier versions
// in args (before Prepare): -local_type=p2p|p2ps|p2pt|secret,
// -target_type, -fallback_secret and -disable_p2p become -local_type and
// -conn_mode, combined as config.LegacyLocal does for npc.conf. Other
// arguments stay as they are. Remove in the next major version.
func NPCLocal(args []string) (out, warnings []string) {
	var localType, targetType string
	fallback, disable := true, false
	newType, hasConnMode := "", false
	used := []string{}
	out = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			out = append(out, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		name = normalize(name)
		takeValue := func() string {
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				return args[i]
			}
			return value
		}
		boolValue := func() bool {
			if !hasValue {
				return true
			}
			b, err := strconv.ParseBool(value)
			return err != nil || b
		}
		switch name {
		case "local_type":
			v := takeValue()
			switch lv := strings.ToLower(strings.TrimSpace(v)); lv {
			case "p2p", "p2ps", "p2pt", "secret":
				localType = lv
				used = append(used, "-local_type="+lv)
			default:
				newType = v
			}
		case "target_type":
			targetType = strings.ToLower(strings.TrimSpace(takeValue()))
			used = append(used, "-target_type")
		case "fallback_secret":
			fallback = boolValue()
			used = append(used, "-fallback_secret")
		case "disable_p2p":
			disable = boolValue()
			used = append(used, "-disable_p2p")
		case "conn_mode":
			hasConnMode = true
			out = append(out, a)
		default:
			out = append(out, a)
		}
	}
	if len(used) == 0 {
		if newType != "" {
			out = append(out, "--local_type="+newType)
		}
		return out, nil
	}
	typ, mode := config.LegacyLocal(true, localType, targetType, fallback)
	if disable {
		mode = config.ConnRelay
	}
	if newType != "" {
		typ = newType
	}
	out = append(out, "--local_type="+typ)
	if !hasConnMode {
		out = append(out, "--conn_mode="+mode)
	}
	warnings = append(warnings, "\""+strings.Join(used, " ")+"\": old point-to-point access flags, used as -local_type="+typ+" -conn_mode="+mode)
	return out, warnings
}
