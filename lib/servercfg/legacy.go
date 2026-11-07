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
}

var legacyKeys = []legacyKey{
	{old: "tls_bridge_port", current: "bridge_tls_port"},
	{old: "log_color", note: "log_color was removed: log lines are plain logfmt or JSON (log_format)"},
	{old: "ip_limit", note: "ip_limit was removed: it made every client accept only visitors whose address " +
		"was registered (npc register). Registration-only access is now set per client, tunnel or host: " +
		"\"Registered IPs Only\" in the web UI, or registered_only=1 in the web API"},
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
		if _, err := lookupValue(values, l.old); err != nil {
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
