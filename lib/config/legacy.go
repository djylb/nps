package config

import (
	"strings"

	"github.com/djylb/nps/lib/logs"
)

// This file is the one place npc.conf keys and sections of earlier versions
// are handled, so that a config written for an earlier npc keeps working,
// or says what to do instead, after an update.

// legacyCommon is what the legacy keys of a [common] section asked for.
type legacyCommon struct {
	tlsEnable bool
}

// read takes a [common] key earlier versions read and this one does not,
// reporting whether item was one.
func (l *legacyCommon) read(item [2]string) bool {
	switch item[0] {
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
		logs.Warn("npc.conf: tls_enable=true is the old spelling of conn_type=tls; connecting with tls, not %s", c.Tp)
	}
	c.Tp = "tls"
}

// legacySection returns a warning for a section earlier versions read and
// this one ignores, or "" when the section is not one.
func legacySection(name string, keys map[string]bool) string {
	for k := range keys {
		if strings.HasPrefix(k, "health_") {
			return "Ignoring section [" + name + "]: npc no longer runs health checks from npc.conf; " +
				"set them on the server per tunnel or host (web UI, or health_check* of the web API)"
		}
	}
	return ""
}
