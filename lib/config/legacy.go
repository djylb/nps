package config

import (
	"strings"

	"github.com/djylb/nps/lib/model"
)

// This file is the one place npc.conf keys and sections of earlier versions
// are handled, so that a config written for an earlier npc keeps working,
// or says what to do instead, after an update. Remove it, and its calls,
// in the next major version.

// legacyCommon is what the legacy keys of a [common] section asked for.
type legacyCommon struct {
	tlsEnable bool
}

// read takes a [common] key earlier versions read and this one does not,
// reporting whether item was one.
func (l *legacyCommon) read(item [2]string) bool {
	switch item[0] {
	case "new_protocol":
		log.Warn("legacy config setting ignored", "keys", "new_protocol", "detail", "npc always uses the core protocol")
		return true
	case "tls_enable":
		// Up to 0.34, tls_enable=true made the connection type tls,
		// whatever conn_type said.
		l.tlsEnable = parseBool(item)
		return true
	}
	return false
}

// apply applies what the legacy keys asked for once the whole section is
// read.
func (l *legacyCommon) apply(c *CommonConfig) {
	if !l.tlsEnable {
		return
	}
	if !strings.EqualFold(c.Tp, "tls") {
		log.Warn("legacy config setting", "keys", "tls_enable", "conn_type", c.Tp, "detail", "tls_enable=true is the old spelling of conn_type=tls; connecting with tls")
	}
	c.Tp = "tls"
}

// legacySection returns a warning for a section earlier versions read and
// this one ignores, or "" when the section is not one.
func legacySection(keys map[string]bool) string {
	for k := range keys {
		if strings.HasPrefix(k, "health_") {
			return "npc no longer runs health checks from npc.conf; " +
				"set them on the server per tunnel or host (web UI, or health_check* of the web API)"
		}
	}
	return ""
}

// legacyLocalSection reports whether a section is a point-to-point access
// listener of an earlier version: [p2p...] without a mode key.
func legacyLocalSection(name string, keys map[string]bool) bool {
	return strings.HasPrefix(name, "p2p") && !keys["mode"]
}

// legacyLocal collects the old keys of a point-to-point access section:
// local_type values secret/p2p/p2ps/p2pt, target_type and fallback_secret.
type legacyLocal struct {
	section     bool // a [p2p...] section
	fallback    bool
	hasFallback bool
	localType   string
	targetType  string
	used        []string
}

// read takes an old key, or a local_type with an old value, reporting
// whether item was one.
func (l *legacyLocal) read(item [2]string) bool {
	switch item[0] {
	case "local_type":
		v := strings.ToLower(strings.TrimSpace(item[1]))
		switch v {
		case "secret", "p2p", "p2ps", "p2pt":
			l.localType = v
			l.used = append(l.used, "local_type="+v)
			return true
		}
	case "target_type":
		l.targetType = strings.ToLower(strings.TrimSpace(item[1]))
		l.used = append(l.used, "target_type")
		return true
	case "fallback_secret":
		l.fallback, l.hasFallback = parseBool(item), true
		l.used = append(l.used, "fallback_secret")
		return true
	}
	return false
}

// apply sets the type and connection mode of ls from the old keys, unless
// local_type or conn_mode were given in the current spelling, and warns.
func (l *legacyLocal) apply(name string, ls *LocalServer) bool {
	if !l.section && len(l.used) == 0 {
		if ls.Type != "" || ls.ConnMode != "" {
			return false
		}
		// A [secret] section of an earlier version, with neither local_type
		// nor conn_mode: it was relayed, and stays so rather than starting
		// to punch holes after the update.
		ls.Type, ls.ConnMode = LocalTCPUDP, ConnRelay
		log.Warn("legacy config setting", "name", name, "keys", "["+name+"]",
			"detail", "a [secret...] section without conn_mode stays relayed; add conn_mode=auto to try direct connections")
		return true
	}
	typ, mode := LegacyLocal(l.section, l.localType, l.targetType, !l.hasFallback || l.fallback)
	if ls.Type == "" {
		ls.Type = typ
	}
	if ls.ConnMode == "" {
		ls.ConnMode = mode
	}
	used := l.used
	if l.section {
		used = append([]string{"[" + name + "]"}, used...)
	}
	log.Warn("legacy config setting", "name", name, "keys", strings.Join(used, ","),
		"detail", "use a [secret...] section with local_type="+ls.Type+" and conn_mode="+ls.ConnMode)
	return true
}

// LegacyLocal maps the old settings to a listener type and connection
// mode: p2p is direct with relay fallback (auto), or direct only when
// fallback_secret=false; secret is relayed. target_type tcp/udp/all chose
// the listeners of p2p and secret.
func LegacyLocal(p2pSection bool, localType, targetType string, fallback bool) (typ, mode string) {
	typ = LocalTCPUDP
	switch targetType {
	case "tcp":
		typ = LocalTCP
	case "udp":
		typ = LocalUDP
	}
	switch localType {
	case "p2ps":
		typ = LocalMixed
	case "p2pt":
		typ = LocalTransparent
	case "secret":
		return typ, ConnRelay
	case "":
		if !p2pSection {
			return typ, ConnRelay
		}
	}
	if !fallback {
		return typ, ConnDirect
	}
	return typ, ConnAuto
}

// legacyTunnelMode turns a provider task of mode p2p into the secret task
// it now is; the password stays, so visitors keep working.
func legacyTunnelMode(t *model.Tunnel) bool {
	if t.Mode != "p2p" {
		return false
	}
	log.Warn("legacy config setting", "keys", "mode=p2p", "detail", "p2p tasks are secret tasks (point-to-point access) now; use mode=secret")
	t.Mode = "secret"
	return true
}

// UsesLegacyPointToPoint reports whether c held point-to-point access
// settings of an earlier version, which were converted. Launch payloads,
// never released with them, refuse such a config.
func (c *Config) UsesLegacyPointToPoint() bool { return c.legacy }

// ConvertFileTunnels applies model.ConvertFileTunnel to the tunnels of c:
// a file tunnel with a listening port becomes a virtual backend and a tcp
// tunnel to it. It is idempotent.
func (c *Config) ConvertFileTunnels() {
	var out []*model.Tunnel
	for _, t := range c.Tasks {
		cs, ok := model.ConvertFileTunnel(t)
		out = append(out, t)
		if ok && len(cs) > 0 {
			log.Warn("legacy config setting", "keys", "server_port", "remark", t.Remark, "detail", "file tunnel converted to a virtual backend and a tcp tunnel to it")
		}
		out = append(out, cs...)
	}
	c.Tasks = out
}
