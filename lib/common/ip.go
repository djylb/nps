package common

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func BytesToNum(b []byte) int {
	var str string
	for i := 0; i < len(b); i++ {
		str += strconv.Itoa(int(b[i]))
	}
	x, _ := strconv.Atoi(str)
	return x
}

func GetExtFromPath(path string) string {
	s := strings.Split(path, ".")
	re, err := regexp.Compile(`(\w+)`)
	if err != nil {
		return ""
	}
	return string(re.Find([]byte(s[0])))
}

func GetLocalUdp4IP() (net.IP, error) {
	c, err := net.Dial("udp4", IPv4DNS)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	la, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || la == nil || la.IP == nil {
		return nil, fmt.Errorf("get local udp4 ip failed")
	}
	return la.IP, nil
}

func GetLocalUdp6IP() (net.IP, error) {
	c, err := net.Dial("udp6", IPv6DNS)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	la, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || la == nil || la.IP == nil {
		return nil, fmt.Errorf("get local udp6 ip failed")
	}
	return la.IP, nil
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

func GetMatchingLocalAddr(remoteAddr, localAddr string) (string, error) {
	remoteIsV6 := strings.Contains(remoteAddr, "]:")
	localIsV6 := strings.Contains(localAddr, "]:")
	if remoteIsV6 == localIsV6 {
		return localAddr, nil
	}
	port := GetPortStrByAddr(localAddr)
	if remoteIsV6 {
		tmpConn, err := GetLocalUdp6Addr()
		if err != nil {
			return localAddr, fmt.Errorf("get local ipv6 addr: %w", err)
		}
		ip6 := tmpConn.LocalAddr().(*net.UDPAddr).IP.String()
		return "[" + ip6 + "]:" + port, nil
	}
	tmpConn, err := GetLocalUdp4Addr()
	if err != nil {
		return localAddr, fmt.Errorf("get local ipv4 addr: %w", err)
	}
	ip4 := tmpConn.LocalAddr().(*net.UDPAddr).IP.String()
	return ip4 + ":" + port, nil
}

var externalIp string
var ipApis = []string{"https://4.ipw.cn", "https://api.ipify.org", "http://ipinfo.io/ip", "https://api64.ipify.org", "https://6.ipw.cn", "http://api.ip.sb", "http://myexternalip.com/raw", "http://ifconfig.me/ip", "http://ident.me", "https://d-jy.net/ip"}

func FetchExternalIp() string {
	for _, api := range ipApis {
		resp, err := http.Get(api)
		if err != nil {
			continue
		}
		content, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		ip := string(content)
		if IsValidIP(ip) {
			externalIp = ip
			return ip
		}
	}
	return ""
}

func GetExternalIp() string {
	if externalIp != "" {
		return externalIp
	}
	return FetchExternalIp()
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

func GetIntranetIp() string { /* unchanged */
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil || ipnet.IP.To16() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", GetCustomDNS())
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).IP
}

func GetOutboundIPv6() net.IP {
	tmpConn, err := GetLocalUdp6Addr()
	if err == nil {
		return tmpConn.LocalAddr().(*net.UDPAddr).IP
	}
	return nil
}

func IsValidIP(ip string) bool { return net.ParseIP(ip) != nil }

func IsPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10, ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31, ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}
	if ip6 := ip.To16(); ip6 != nil {
		return !ip6.IsPrivate()
	}
	return false
}

func GetServerIp(ip string) string { /* unchanged */
	if ip != "" && ip != "0.0.0.0" && ip != "::" {
		return ip
	}
	if ip == "::" {
		tmpConn, err := GetLocalUdp6Addr()
		if err == nil {
			return tmpConn.LocalAddr().(*net.UDPAddr).IP.String()
		}
	}
	return GetOutboundIP().String()
}

func GetServerIpByClientIp(clientIp net.IP) string {
	if IsPublicIP(clientIp) {
		return GetExternalIp()
	}
	return GetIntranetIp()
}

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

func JoinHostPort(host string, port string) string { return net.JoinHostPort(host, port) }

func ParseIPFromAddr(addr string) net.IP {
	if addr == "" {
		return nil
	}
	ipStr := GetIpByAddr(addr)
	if ipStr == "" {
		return nil
	}
	if strings.HasPrefix(ipStr, "[") && strings.HasSuffix(ipStr, "]") {
		ipStr = ipStr[1 : len(ipStr)-1]
	}
	if i := strings.LastIndex(ipStr, "%"); i != -1 {
		ipStr = ipStr[:i]
	}
	return net.ParseIP(ipStr)
}

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

func IsPublicIPStrict(ip net.IP) bool { /* unchanged */
	if ip == nil {
		return false
	}
	ip = NormalizeIP(ip)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 || ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2 || ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100 || ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 || ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) || ip4[0] == 192 && ip4[1] == 88 && ip4[2] == 99 {
			return false
		}
		return true
	}
	if len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
		return false
	}
	return true
}

func PickBestV4V6FromLocalList(localStr string) (bestV4 string, bestV6 string, fallback string) { /* unchanged */
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

func HasIPv6InLocalList(localStr string) bool {
	_, v6, _ := PickBestV4V6FromLocalList(localStr)
	return v6 != ""
}

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
