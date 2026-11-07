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

// RemovePortFromHost
// return "[2001:db8::1]"
func RemovePortFromHost(host string) string {
	if len(host) == 0 {
		return host
	}
	var idx int
	// IPv6
	if host[0] == '[' {
		if idx = strings.IndexByte(host, ']'); idx != -1 {
			return host[:idx+1]
		}
		return ""
	}
	// IPv4 or Domain
	if idx = strings.LastIndexByte(host, ':'); idx != -1 && idx == strings.IndexByte(host, ':') {
		return host[:idx]
	}
	return host
}

// GetIpByAddr
// return "2001:db8::1"
func GetIpByAddr(host string) string {
	if len(host) == 0 {
		return host
	}
	var idx int
	// IPv6
	if host[0] == '[' {
		if idx = strings.IndexByte(host, ']'); idx != -1 {
			return host[1:idx]
		}
		return ""
	}
	// IPv4 or Domain
	if idx = strings.LastIndexByte(host, ':'); idx != -1 && idx == strings.IndexByte(host, ':') {
		return host[:idx]
	}
	return host
}

func isDomain(s string) bool {
	return net.ParseIP(s) == nil
}

// GetPortByAddr
// return int or 0
func getPortByAddr(addr string) int {
	if len(addr) == 0 {
		return 0
	}
	// IPv6
	if addr[0] == '[' {
		if end := strings.IndexByte(addr, ']'); end != -1 && end+1 < len(addr) && addr[end+1] == ':' {
			if port, ok := parsePortString(addr[end+2:]); ok {
				return port
			}
		}
		return 0
	}
	// Other
	if idx := strings.LastIndexByte(addr, ':'); idx != -1 {
		if port, ok := parsePortString(addr[idx+1:]); ok {
			return port
		}
	}
	return 0
}

func GetPortStrByAddr(addr string) string {
	port := getPortByAddr(addr)
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
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
	sni := GetIpByAddr(host)
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
	for _, item := range strings.Split(s, ",") {
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

// ParseIPFromAddr parses IP from an "ip:port" (supports "[v6]:port").
// Returns nil if cannot parse.
func ParseIPFromAddr(addr string) net.IP {
	if addr == "" {
		return nil
	}
	ipStr := GetIpByAddr(addr)
	if ipStr == "" {
		return nil
	}
	if i := strings.LastIndexByte(ipStr, '%'); i != -1 { // fe80::1%eth0
		ipStr = ipStr[:i]
	}
	return net.ParseIP(ipStr)
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
	host := GetIpByAddr(addr)
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
