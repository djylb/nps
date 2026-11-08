// Package netaddr parses host and port strings, picks local and outbound
// IP addresses, builds bind addresses and routes lookups to a custom DNS
// server.
package netaddr

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/netx"
)

func parsePortString(s string) (int, bool) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return 0, false
	}
	return p, true
}

func dialLocalUDPAddr(network, target string) (*net.UDPAddr, error) {
	c, err := net.Dial(network, target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()

	la, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || la == nil || la.IP == nil {
		return nil, fmt.Errorf("get local %s addr failed", network)
	}

	ip := make(net.IP, len(la.IP))
	copy(ip, la.IP)

	return &net.UDPAddr{
		IP:   ip,
		Port: la.Port,
		Zone: la.Zone,
	}, nil
}

// ExtractHost
// return "[2001:db8::1]:80"
func ExtractHost(input string) string {
	if strings.Contains(input, "://") {
		if u, err := url.Parse(input); err == nil && u.Host != "" {
			return u.Host
		}
	}
	if idx := strings.IndexByte(input, '/'); idx != -1 {
		input = input[:idx]
	}
	return input
}

// Host returns the host of s, an address given as "host", "host:port",
// "[ipv6]" or "[ipv6]:port", without brackets and with any zone kept. A bare
// IPv6 address such as "2001:db8::1" is a host without a port. The port is
// not checked. It returns "" for an empty host or an unclosed or trailing
// bracket, such as "[::1" or "[::1]x".
func Host(s string) string {
	host, _ := splitHost(s)
	return host
}

// Port returns the port of s, given as for Host, or 0 when s has no port in
// 1-65535.
func Port(s string) int {
	_, port := splitHost(s)
	p, ok := parsePortString(port)
	if !ok {
		return 0
	}
	return p
}

// splitHost splits s as described at Host, returning the port text as given.
func splitHost(s string) (host, port string) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	if strings.HasPrefix(s, "[") {
		if strings.HasSuffix(s, "]") && strings.IndexByte(s, ']') == len(s)-1 {
			return s[1 : len(s)-1], ""
		}
		return "", ""
	}
	return s, ""
}

// ParseIP reads an IP address given alone or as host:port, with IPv6 in
// brackets and a port in 0-65535. A zone and an IPv4-mapped IPv6 form are
// kept.
func ParseIP(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip, true
	}
	return netip.Addr{}, false
}

func isDomain(s string) bool {
	return net.ParseIP(s) == nil
}

func SplitServerAndPath(s string) (server, path string) {
	index := strings.IndexByte(s, '/')
	if index == -1 {
		return s, ""
	}
	return s[:index], s[index:]
}

func SplitAddrAndHost(s string) (addr, host, sni string) {
	s = strings.TrimSpace(s)
	index := strings.IndexByte(s, '@')
	if index == -1 {
		return s, s, getSni(s)
	}
	addr = strings.TrimSpace(s[:index])
	host = strings.TrimSpace(s[index+1:])
	if host == "" {
		return addr, addr, ""
	}
	return addr, host, getSni(host)
}

func getSni(host string) string {
	sni := Host(host)
	if !isDomain(sni) {
		return ""
	}
	return sni
}

// GetPorts format ports str to an int array
func GetPorts(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	seen := make(map[int]struct{})
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if fw := strings.SplitN(item, "-", 2); len(fw) == 2 {
			a, b := strings.TrimSpace(fw[0]), strings.TrimSpace(fw[1])
			start, ok1 := parsePortString(a)
			end, ok2 := parsePortString(b)
			if ok1 && ok2 {
				if end < start {
					start, end = end, start
				}
				for i := start; i <= end; i++ {
					seen[i] = struct{}{}
				}
			}
			continue
		}
		if port, ok := parsePortString(item); ok {
			seen[port] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	ps := make([]int, 0, len(seen))
	for p := range seen {
		ps = append(ps, p)
	}
	sort.Ints(ps)
	return ps
}

// FormatAddress if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return net.JoinHostPort("127.0.0.1", s)
}

// localUDPAddr returns the local address the system picks to reach the DNS
// server.
func localUDPAddr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp", customDNS())
}

func localUDP4Addr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp4", ipv4DNS)
}

func localUDP6Addr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp6", ipv6DNS)
}

// LocalUDP4IP returns the preferred local IPv4 egress IP.
func LocalUDP4IP() (net.IP, error) {
	addr, err := localUDP4Addr()
	if err != nil {
		return nil, err
	}
	return addr.IP, nil
}

// LocalUDP6IP returns the preferred local IPv6 egress IP.
func LocalUDP6IP() (net.IP, error) {
	addr, err := localUDP6Addr()
	if err != nil {
		return nil, err
	}
	return addr.IP, nil
}

func NormalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip.To16()
}

func IsZeroIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.Equal(net.IPv4zero) || ip.Equal(net.IPv6zero)
}

func PickEgressIPFor(dstIP net.IP) net.IP {
	if dstIP == nil {
		return nil
	}
	network := "udp4"
	if dstIP.To4() == nil {
		network = "udp6"
	}
	raddr := (&net.UDPAddr{IP: dstIP, Port: 9}).String()
	d := net.Dialer{Timeout: 300 * time.Millisecond}
	conn, err := d.Dial(network, raddr)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	if la, ok := conn.LocalAddr().(*net.UDPAddr); ok && la != nil && !IsZeroIP(la.IP) {
		return la.IP
	}
	return nil
}

func GetOutboundIP() net.IP {
	addr, err := localUDPAddr()
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	return addr.IP
}

func GetOutboundIPv4() net.IP {
	addr, err := localUDP4Addr()
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	return addr.IP
}

func GetOutboundIPv6() net.IP {
	addr, err := localUDP6Addr()
	if err != nil {
		return net.ParseIP("::1")
	}
	return addr.IP
}

func GetServerIp(ip string) string {
	if ip != "" && ip != "0.0.0.0" && ip != "::" {
		return ip
	}
	if ip == "::" {
		ip6, err := LocalUDP6IP()
		if err == nil {
			return ip6.String()
		}
	}
	return GetOutboundIP().String()
}

func BuildTCPBindAddr(localIP string) net.Addr {
	ip := net.ParseIP(strings.TrimSpace(localIP))
	if ip == nil {
		return nil
	}
	return &net.TCPAddr{IP: ip}
}

func BuildUDPBindAddr(localIP string) *net.UDPAddr {
	ip := net.ParseIP(strings.TrimSpace(localIP))
	if ip == nil {
		return nil
	}
	return &net.UDPAddr{IP: ip}
}

func IsPublicHost(addr string) bool {
	host := Host(addr)
	if host == "" {
		return false
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return netx.IsGlobal(a)
	}
	return true
}

// Public DNS servers that local and outbound addresses are picked against.
const (
	ipv4DNS = "8.8.8.8:53"
	ipv6DNS = "[2400:3200::1]:53"
)
