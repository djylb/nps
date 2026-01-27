package common

import (
	"net"
	"strings"
)

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

// WhitelistAllows reports whether addr is allowed by the given whitelist entries.
//
// Important: This function intentionally does NOT resolve hostnames to IPs.
// If addr contains a hostname, only hostname and wildcard entries are considered.
// If addr contains an IP, IP and CIDR entries are considered.
func WhitelistAllows(entries []string, addr string) bool {
	if len(entries) == 0 {
		return false
	}

	hostPort := ExtractHost(addr)
	hostOnly := RemovePortFromHost(hostPort)
	if hostOnly == "" {
		return false
	}

	// Detect whether destination is IP.
	var dstIP net.IP
	if strings.HasPrefix(hostOnly, "[") {
		dstIP = net.ParseIP(GetIpByAddr(hostOnly))
	} else {
		dstIP = net.ParseIP(hostOnly)
	}

	if dstIP != nil {
		for _, e := range entries {
			if strings.Contains(e, "/") {
				_, cidr, err := net.ParseCIDR(e)
				if err != nil {
					continue
				}
				if cidr.Contains(dstIP) {
					return true
				}
				continue
			}
			if ip := net.ParseIP(e); ip != nil {
				if ip.Equal(dstIP) {
					return true
				}
			}
		}
		return false
	}

	// Hostname match (case-insensitive).
	host := strings.ToLower(hostOnly)
	for _, e := range entries {
		if e == host {
			return true
		}
		if strings.HasPrefix(e, "*.") {
			suffix := strings.TrimPrefix(e, "*.")
			if suffix == "" {
				continue
			}
			// Require a subdomain: x.suffix (not equal to suffix).
			if host == suffix {
				continue
			}
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		}
	}
	return false
}
