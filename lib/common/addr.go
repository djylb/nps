package common

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	domainCheckWithPathRegexp = regexp.MustCompile(`^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,6}(/)`)
	domainCheckRegexp         = regexp.MustCompile(`^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,6}`)
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

func IsDomain(s string) bool {
	return net.ParseIP(s) == nil
}

// GetPortByAddr
// return int or 0
func GetPortByAddr(addr string) int {
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
	port := GetPortByAddr(addr)
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func ValidateAddr(s string) string {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil {
		return ""
	}
	if _, ok := parsePortString(port); !ok {
		return ""
	}
	return s
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
		return s, s, GetSni(s)
	}
	addr = strings.TrimSpace(s[:index])
	host = strings.TrimSpace(s[index+1:])
	if host == "" {
		return addr, addr, ""
	}
	return addr, host, GetSni(host)
}

func GetSni(host string) string {
	sni := GetIpByAddr(host)
	if !IsDomain(sni) {
		return ""
	}
	return sni
}

// DomainCheck Check the legality of domain
func DomainCheck(domain string) bool {
	return domainCheckWithPathRegexp.MatchString(domain) || domainCheckRegexp.MatchString(domain)
}

func GetPort(value int) int {
	if value >= 0 {
		return value % 65536
	}
	return (65536 + value%65536) % 65536
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

// IsPort is the string a port
func IsPort(p string) bool {
	_, ok := parsePortString(p)
	return ok
}

// FormatAddress if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return net.JoinHostPort("127.0.0.1", s)
}

// GetLocalUdpAddr send this ip forget to get a local udp port
func GetLocalUdpAddr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp", GetCustomDNS())
}

func GetLocalUdp4Addr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp4", IPv4DNS)
}

func GetLocalUdp6Addr() (*net.UDPAddr, error) {
	return dialLocalUDPAddr("udp6", IPv6DNS)
}

// GetLocalUdp4IP returns the preferred local IPv4 egress IP.
func GetLocalUdp4IP() (net.IP, error) {
	addr, err := GetLocalUdp4Addr()
	if err != nil {
		return nil, err
	}
	return addr.IP, nil
}

// GetLocalUdp6IP returns the preferred local IPv6 egress IP.
func GetLocalUdp6IP() (net.IP, error) {
	addr, err := GetLocalUdp6Addr()
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

func BuildUdpBindAddr(serverIP string, clientIP net.IP) (network string, addr *net.UDPAddr) {
	if ip := net.ParseIP(serverIP); ip != nil && !IsZeroIP(ip) {
		if ip.To4() != nil {
			return "udp4", &net.UDPAddr{IP: ip, Port: 0}
		}
		return "udp6", &net.UDPAddr{IP: ip, Port: 0}
	}
	if clientIP != nil {
		if clientIP.To4() != nil {
			return "udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0}
		}
		return "udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: 0}
	}
	return "udp", &net.UDPAddr{IP: nil, Port: 0}
}

func IsSameIPType(addr1, addr2 string) bool {
	ip1 := strings.Contains(addr1, "[")
	ip2 := strings.Contains(addr2, "[")
	return ip1 == ip2
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
	addr, err := GetLocalUdpAddr()
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	return addr.IP
}

func GetOutboundIPv4() net.IP {
	addr, err := GetLocalUdp4Addr()
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	return addr.IP
}

func GetOutboundIPv6() net.IP {
	addr, err := GetLocalUdp6Addr()
	if err != nil {
		return net.ParseIP("::1")
	}
	return addr.IP
}

func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}
	// IPv6
	if ip6 := ip.To16(); ip6 != nil {
		return !ip6.IsPrivate()
	}
	return false
}

func GetServerIp(ip string) string {
	if ip != "" && ip != "0.0.0.0" && ip != "::" {
		return ip
	}
	if ip == "::" {
		ip6, err := GetLocalUdp6IP()
		if err == nil {
			return ip6.String()
		}
	}
	return GetOutboundIP().String()
}

// EncodeIP encodes a net.IP to [1-byte ATYP] + [16-byte Address]
func EncodeIP(ip net.IP) []byte {
	buf := make([]byte, 17)
	if ip4 := ip.To4(); ip4 != nil {
		buf[0] = 0x01
		copy(buf[1:], ip4)
	} else {
		buf[0] = 0x04
		copy(buf[1:], ip.To16())
	}
	return buf
}

// DecodeIP decodes a [1-byte ATYP] + [16-byte Address] to net.IP
func DecodeIP(data []byte) net.IP {
	if len(data) < 17 {
		return nil
	}
	atyp := data[0]
	addr := data[1:17]
	switch atyp {
	case 0x01:
		return net.IPv4(addr[0], addr[1], addr[2], addr[3])
	case 0x04:
		return addr
	default:
		return nil
	}
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

// SplitCommaAddrList splits "ip:port,ip:port,..." into a de-duplicated list of valid IP:port.
// - Accepts single item without comma.
// - Ignores invalid entries.
// - Keeps first valid one as stable fallback.
func SplitCommaAddrList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v := ValidateAddr(p)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// IsPublicIPStrict checks if IP is globally routable on the Internet.
// It excludes RFC1918, loopback, link-local, multicast, unspecified,
// and also excludes CGNAT / TEST-NET / benchmarking / documentation ranges.
func IsPublicIPStrict(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip = NormalizeIP(ip)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// CGNAT 100.64.0.0/10
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
		// TEST-NET-1/2/3: 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2 {
			return false
		}
		if ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100 {
			return false
		}
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return false
		}
		// 198.18.0.0/15 benchmark
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return false
		}
		// 192.88.99.0/24 (6to4 relay anycast, deprecated)
		if ip4[0] == 192 && ip4[1] == 88 && ip4[2] == 99 {
			return false
		}
		return true
	}
	// IPv6 documentation: 2001:db8::/32
	if len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return false
	}
	return true
}

// PickBestV4V6FromLocalList picks best IPv4/IPv6 address from localStr (comma-separated).
// Rules within each family:
// - First seen becomes candidate.
// - If later a "public" address appears and current is not public, replace it.
// Returns:
// - bestV4: best IPv4 "ip:port" if any
// - bestV6: best IPv6 "[ip]:port" if any
// - fallback: first valid address from list (stable)
func PickBestV4V6FromLocalList(localStr string) (bestV4 string, bestV6 string, fallback string) {
	addrs := SplitCommaAddrList(localStr)
	if len(addrs) == 0 {
		return "", "", ""
	}
	fallback = addrs[0]
	var bestV4Public, bestV6Public bool
	for _, a := range addrs {
		ip := ParseIPFromAddr(a)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			pub := IsPublicIPStrict(ip)
			if bestV4 == "" || (!bestV4Public && pub) {
				bestV4, bestV4Public = a, pub
			}
			continue
		}
		pub := IsPublicIPStrict(ip)
		if bestV6 == "" || (!bestV6Public && pub) {
			bestV6, bestV6Public = a, pub
		}
	}
	return
}

// HasIPv6InLocalList reports whether localStr contains at least one valid IPv6 "[...]:port".
func HasIPv6InLocalList(localStr string) bool {
	_, v6, _ := PickBestV4V6FromLocalList(localStr)
	return v6 != ""
}

// ChooseLocalAddrForPeer chooses ONE address from selfLocal to return to peer.
// Rule:
// - If both sides contain IPv6 -> return self IPv6 (best, public preferred)
// - Else -> return self IPv4 (best, public preferred)
// - Else -> return self IPv6 if any
// - Else -> return fallback (first valid) or ""
func ChooseLocalAddrForPeer(selfLocal, peerLocal string) string {
	selfV4, selfV6, selfFallback := PickBestV4V6FromLocalList(selfLocal)
	peerHasV6 := HasIPv6InLocalList(peerLocal)
	if selfV6 != "" && peerHasV6 {
		return selfV6
	}
	if selfV4 != "" {
		return selfV4
	}
	if selfV6 != "" {
		return selfV6
	}
	return selfFallback
}

// FixUdpListenAddrForRemote makes sure localAddr is a concrete address (not wildcard)
// and matches the IP family of remoteAddr.
// Returns (network, fixedLocalAddr).
func FixUdpListenAddrForRemote(remoteAddr, localAddr string) (string, string, error) {
	rip := ParseIPFromAddr(remoteAddr)
	if rip == nil {
		return "", "", fmt.Errorf("parse remote ip failed: %s", remoteAddr)
	}
	rip = NormalizeIP(rip)
	wantV4 := rip.To4() != nil
	network := "udp6"
	if wantV4 {
		network = "udp4"
	}
	port := GetPortStrByAddr(localAddr)
	if port == "" || port == "0" {
		return "", "", fmt.Errorf("invalid local port: %s", localAddr)
	}
	lip := ParseIPFromAddr(localAddr)
	if lip != nil {
		lip = NormalizeIP(lip)
	}
	localSpecified := lip != nil && !IsZeroIP(lip) && !lip.IsUnspecified()
	localIsV4 := lip != nil && lip.To4() != nil
	if localSpecified && (localIsV4 == wantV4) {
		return network, localAddr, nil
	}
	var ip net.IP
	var err error
	if wantV4 {
		ip, err = GetLocalUdp4IP()
	} else {
		ip, err = GetLocalUdp6IP()
	}
	if err != nil {
		return "", "", err
	}
	ip = NormalizeIP(ip)
	if ip == nil || IsZeroIP(ip) || ip.IsUnspecified() {
		return "", "", fmt.Errorf("no usable local ip for %s", network)
	}
	return network, net.JoinHostPort(ip.String(), port), nil
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
	if ip := net.ParseIP(host); ip != nil {
		return IsPublicIPStrict(ip)
	}
	return true
}

func BuildAddress(host string, port string) string {
	return net.JoinHostPort(host, port)
}
