package common

import (
	"net"
	"sort"
	"strings"

	"github.com/djylb/nps/lib/logs"
)

// ProxyACL is an allow-list matcher for proxy destinations.
// Enabled when Entries is non-empty; deny-by-default.
//
// Entry formats (one per line):
// - IP:        1.2.3.4, 2001:db8::1
// - CIDR:      10.0.0.0/8, 2001:db8::/32
// - Hostname:  example.com
// - Host:Port: example.com:443, [2001:db8::1]:443 (port is ignored)
// - Wildcard:  *.example.com  (subdomains only; NOT include example.com)
// - Wildcard:  *example.com   (include example.com + subdomains)
type ProxyACL struct {
	Entries []string

	ExactIPs  map[string]struct{} // ip.String() => {}
	CIDRs     []*net.IPNet
	Hostnames map[string]struct{} // exact hostname (lower)

	// ".a.com" => subdomain-only (from "*.a.com")
	// "a.com"  => include root + subdomains (from "*a.com")
	WildcardSuffixes []string
}

// ParseProxyACLEntries parses newline-separated text, trims and lowercases.
// Lines starting with '#' or ';' are ignored.
func ParseProxyACLEntries(raw string) []string {
	if raw == "" {
		return nil
	}

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

// ParseProxyACL builds a matcher from raw entries.
// Note: it does NOT resolve DNS; hostnames match only hostname/wildcard rules.
func ParseProxyACL(raw string) *ProxyACL {
	entries := ParseProxyACLEntries(raw)
	if len(entries) == 0 {
		return &ProxyACL{}
	}

	acl := &ProxyACL{
		Entries: entries,
	}

	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		// CIDR: if it contains '/', only treat it as CIDR. Invalid CIDR is skipped.
		if strings.Contains(e, "/") {
			_, cidr, err := net.ParseCIDR(e)
			if err == nil && cidr != nil {
				acl.CIDRs = append(acl.CIDRs, cidr)
			} else {
				logs.Warn("invalid proxy acl CIDR entry: %s", e)
			}
			continue
		}

		// Wildcards:
		// "*.a.com" => store ".a.com" (subdomain-only)
		// "*a.com"  => store "a.com"  (include root + subdomains)
		if strings.HasPrefix(e, "*") {
			subOnly := strings.HasPrefix(e, "*.")
			suffixRaw := strings.TrimPrefix(e, "*")
			if subOnly {
				suffixRaw = strings.TrimPrefix(e, "*.")
			}
			suffixRaw = strings.TrimSpace(suffixRaw)

			suffix, ok := normalizeHostToken(suffixRaw)
			if !ok || suffix == "" {
				logs.Warn("invalid proxy acl wildcard entry: %s", e)
				continue
			}

			if subOnly {
				acl.WildcardSuffixes = append(acl.WildcardSuffixes, "."+suffix)
			} else {
				acl.WildcardSuffixes = append(acl.WildcardSuffixes, suffix)
			}
			continue
		}

		// IP / Hostname / Host:Port
		host, ok := normalizeHostToken(e)
		if !ok || host == "" {
			logs.Warn("invalid proxy acl hostname entry: %s", e)
			continue
		}

		// Exact IP
		if ip := net.ParseIP(host); ip != nil {
			if acl.ExactIPs == nil {
				acl.ExactIPs = make(map[string]struct{})
			}
			acl.ExactIPs[ip.String()] = struct{}{}
			continue
		}

		// Exact hostname
		if acl.Hostnames == nil {
			acl.Hostnames = make(map[string]struct{})
		}
		acl.Hostnames[host] = struct{}{}
	}

	// Sort suffixes by length desc: longer suffix matches first (minor perf).
	if len(acl.WildcardSuffixes) > 1 {
		sort.Slice(acl.WildcardSuffixes, func(i, j int) bool {
			return len(acl.WildcardSuffixes[i]) > len(acl.WildcardSuffixes[j])
		})
	}

	// Release empty maps/slices to reduce memory footprint.
	if len(acl.ExactIPs) == 0 {
		acl.ExactIPs = nil
	}
	if len(acl.Hostnames) == 0 {
		acl.Hostnames = nil
	}
	if len(acl.CIDRs) == 0 {
		acl.CIDRs = nil
	}
	if len(acl.WildcardSuffixes) == 0 {
		acl.WildcardSuffixes = nil
	}

	return acl
}

func (a *ProxyACL) Enabled() bool {
	return a != nil && len(a.Entries) > 0
}

// Allows checks whether addr is allowed by ACL.
// addr can be: "host:port", "[ipv6]:port", "host", or URL-like forms supported by ExtractHost().
func (a *ProxyACL) Allows(addr string) bool {
	if a == nil || len(a.Entries) == 0 {
		return false
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}

	host := normalizeHostFromAddr(addr)
	if host == "" {
		return false
	}

	// IP
	if ip := net.ParseIP(host); ip != nil {
		if a.ExactIPs != nil {
			if _, ok := a.ExactIPs[ip.String()]; ok {
				return true
			}
		}
		for _, cidr := range a.CIDRs {
			if cidr != nil && cidr.Contains(ip) {
				return true
			}
		}
		return false
	}

	// Exact hostname
	if a.Hostnames != nil {
		if _, ok := a.Hostnames[host]; ok {
			return true
		}
	}

	// Wildcards
	for _, suf := range a.WildcardSuffixes {
		if suf == "" {
			continue
		}

		// "*.a.com" => stored as ".a.com" (subdomains only)
		if suf[0] == '.' {
			if len(host) > len(suf) && strings.HasSuffix(host, suf) {
				return true
			}
			continue
		}

		// "*a.com" => stored as "a.com" (root + subdomains, with boundary)
		if host == suf {
			return true
		}
		if len(host) > len(suf) && strings.HasSuffix(host, suf) {
			prev := len(host) - len(suf) - 1
			if prev >= 0 && host[prev] == '.' {
				return true
			}
		}
	}

	return false
}

// normalizeHostToken normalizes an entry token to a host/ip without port.
// It rejects obvious junk (spaces, URL fragments, path, etc).
func normalizeHostToken(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if strings.IndexAny(s, " \t\r\n") != -1 {
		return "", false
	}
	if strings.IndexAny(s, "?#") != -1 {
		return "", false
	}
	if strings.Contains(s, "/") {
		return "", false
	}

	h := normalizeHostFromAddr(s)
	if h == "" {
		return "", false
	}
	return h, true
}

// normalizeHostFromAddr extracts host/ip from various address forms and normalizes it.
// - keeps IPv6 without brackets
// - lowercases hostnames
// - trims trailing dot
func normalizeHostFromAddr(addr string) string {
	hostPort := ExtractHost(addr)
	hostOnly := RemovePortFromHost(hostPort)
	host := GetIpByAddr(hostOnly)

	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}

	// domain normalization
	host = strings.TrimSuffix(host, ".")
	host = strings.ToLower(host)
	if host == "" {
		return ""
	}
	return host
}
