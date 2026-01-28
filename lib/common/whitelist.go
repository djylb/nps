package common

import (
	"net"
	"strings"

	"github.com/djylb/nps/lib/logs"
)

type WhitelistRuleSet struct {
	Entries   []string
	IPs       []net.IP
	CIDRs     []*net.IPNet
	Hostnames map[string]struct{}
	Wildcards []string
}

// ParseWhitelistEntries parses a newline-separated whitelist string.
//
// Supported entry formats:
// - IP:        1.2.3.4, 2001:db8::1
// - CIDR:      10.0.0.0/8, 2001:db8::/32
// - Hostname:  example.com
// - Wildcard:  *.example.com
//
// Lines starting with '#' or ';' are treated as comments.
func ParseWhitelistEntries(raw string) []string {
	if raw == "" {
		return nil
	}

	// Keep consistent with Target normalization.
	raw = strings.ReplaceAll(raw, "：", ":")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	lines := strings.Split(raw, "\n")
	entries := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		entries = append(entries, strings.ToLower(line))
	}
	return entries
}

// ParseWhitelistRuleSet parses a whitelist string and builds a matcher.
// This does NOT resolve DNS; hostnames only match hostname/wildcard entries.
func ParseWhitelistRuleSet(raw string) *WhitelistRuleSet {
	entries := ParseWhitelistEntries(raw)
	if len(entries) == 0 {
		return &WhitelistRuleSet{}
	}
	set := &WhitelistRuleSet{
		Entries:   entries,
		Hostnames: make(map[string]struct{}),
	}
	for _, e := range entries {
		if strings.Contains(e, "/") {
			_, cidr, err := net.ParseCIDR(e)
			if err == nil && cidr != nil {
				set.CIDRs = append(set.CIDRs, cidr)
			} else {
				logs.Warn("invalid whitelist CIDR entry: %s", e)
			}
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			set.IPs = append(set.IPs, ip)
			continue
		}
		if strings.HasPrefix(e, "*.") {
			suffix := strings.TrimPrefix(e, "*.")
			if suffix != "" {
				set.Wildcards = append(set.Wildcards, suffix)
			} else {
				logs.Warn("invalid whitelist wildcard entry: %s", e)
			}
			continue
		}
		set.Hostnames[e] = struct{}{}
	}
	return set
}

// Allows reports whether addr is allowed by this rule set.
func (w *WhitelistRuleSet) Allows(addr string) bool {
	if w == nil || len(w.Entries) == 0 {
		return false
	}

	hostPort := ExtractHost(addr)
	hostOnly := RemovePortFromHost(hostPort)
	if hostOnly == "" {
		return false
	}

	var dstIP net.IP
	if strings.HasPrefix(hostOnly, "[") {
		dstIP = net.ParseIP(GetIpByAddr(hostOnly))
	} else {
		dstIP = net.ParseIP(hostOnly)
	}

	if dstIP != nil {
		for _, cidr := range w.CIDRs {
			if cidr.Contains(dstIP) {
				return true
			}
		}
		for _, ip := range w.IPs {
			if ip.Equal(dstIP) {
				return true
			}
		}
		return false
	}

	host := strings.ToLower(hostOnly)
	if _, ok := w.Hostnames[host]; ok {
		return true
	}
	for _, suffix := range w.Wildcards {
		if suffix == "" {
			continue
		}
		if host == suffix {
			continue
		}
		if strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
