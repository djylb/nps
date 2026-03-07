package common

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html/template"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/araddon/dateparse"
	"github.com/beevik/ntp"
	"github.com/djylb/nps/lib/logs"
)

var (
	domainCheckWithPathRegexp = regexp.MustCompile(`^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,6}(/)`)
	domainCheckRegexp         = regexp.MustCompile(`^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,6}`)
)

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
			portStr := addr[end+2:]
			if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port <= 65535 {
				return port
			}
		}
		return 0
	}
	// Other
	if idx := strings.LastIndexByte(addr, ':'); idx != -1 {
		portStr := addr[idx+1:]
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port <= 65535 {
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
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return ""
	}
	return s
}

func BuildAddress(host string, port string) string {
	if strings.Contains(host, ":") { // IPv6
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

func SplitServerAndPath(s string) (server, path string) {
	index := strings.Index(s, "/")
	if index == -1 {
		return s, ""
	}
	return s[:index], s[index:]
}

func SplitAddrAndHost(s string) (addr, host, sni string) {
	s = strings.TrimSpace(s)
	index := strings.Index(s, "@")
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
		sni = ""
	}
	return sni
}

// GetHostByName Get the corresponding IP address through domain name
func GetHostByName(hostname string) string {
	if !DomainCheck(hostname) {
		return hostname
	}
	ips, _ := net.LookupIP(hostname)
	if ips != nil {
		for _, v := range ips {
			if v.To4() != nil {
				return v.String()
			}
			// If IPv4 not found, return IPv6
			if v.To16() != nil {
				return v.String()
			}
		}
	}
	return ""
}

// DomainCheck Check the legality of domain
func DomainCheck(domain string) bool {
	return domainCheckWithPathRegexp.MatchString(domain) || domainCheckRegexp.MatchString(domain)
}

func Max(values ...int) int {
	maxVal := math.MinInt
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func Min(values ...int) int {
	minVal := math.MaxInt
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func GetPort(value int) int {
	if value >= 0 {
		return value % 65536
	}
	return (65536 + value%65536) % 65536
}

// CheckAuthWithAccountMap
// u current login user
// p current login passwd
// user global user
// passwd global passwd
// accountMap enable multi user auth
func CheckAuthWithAccountMap(u, p, user, passwd string, accountMap, authMap map[string]string) bool {
	// Single account check
	noAccountMap := len(accountMap) == 0
	noAuthMap := len(authMap) == 0
	if noAccountMap && noAuthMap {
		return u == user && p == passwd
	}

	// Multi-account authentication check
	if len(u) == 0 {
		return false
	}

	if u == user && p == passwd {
		return true
	}

	if !noAccountMap {
		if P, ok := accountMap[u]; ok && p == P {
			return true
		}
	}

	if !noAuthMap {
		if P, ok := authMap[u]; ok && p == P {
			return true
		}
	}

	return false
}

// CheckAuth Check if the Request request is validated
func CheckAuth(r *http.Request, user, passwd string, accountMap, authMap map[string]string) bool {
	// Bypass authentication only if user, passwd are empty and multiAccount is nil or empty
	if user == "" && passwd == "" && len(accountMap) == 0 && len(authMap) == 0 {
		return true
	}

	s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(s) != 2 {
		s = strings.SplitN(r.Header.Get("Proxy-Authorization"), " ", 2)
		if len(s) != 2 {
			return false
		}
	}

	b, err := base64.StdEncoding.DecodeString(s[1])
	if err != nil {
		return false
	}

	pair := strings.SplitN(string(b), ":", 2)
	if len(pair) != 2 {
		return false
	}

	return CheckAuthWithAccountMap(pair[0], pair[1], user, passwd, accountMap, authMap)
}

func DealMultiUser(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	s = strings.ReplaceAll(s, "\r\n", "\n")
	multiUserMap := make(map[string]string)
	for _, v := range strings.Split(s, "\n") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		item := strings.SplitN(v, "=", 2)
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
		}
		multiUserMap[strings.TrimSpace(item[0])] = strings.TrimSpace(item[1])
	}
	return multiUserMap
}

// GetBoolByStr get bool by str
func GetBoolByStr(s string) bool {
	switch s {
	case "1", "true":
		return true
	}
	return false
}

// GetStrByBool get str by bool
func GetStrByBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// GetIntNoErrByStr int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// GetTimeNoErrByStr time
func GetTimeNoErrByStr(str string) time.Time {
	// 1. 去除前后空格
	str = strings.TrimSpace(str)
	if str == "" {
		return time.Time{} // 为空时返回零时间
	}

	// 2. 先尝试解析为 Unix 时间戳（秒或毫秒）
	if timestamp, err := strconv.ParseInt(str, 10, 64); err == nil {
		// 处理毫秒级时间戳
		if timestamp > 1_000_000_000_000 {
			return time.UnixMilli(timestamp)
		}
		// 处理秒级时间戳
		return time.Unix(timestamp, 0)
	}

	// 3. 使用 dateparse 库解析日期字符串
	t, err := dateparse.ParseLocal(str)
	if err == nil {
		return t
	}

	// 解析失败，返回零时间
	return time.Time{}
}

func ContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// ReadAllFromFile Read file content by file path
func ReadAllFromFile(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func GetPath(filePath string) string {
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	path, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}
	return path
}

func GetCertContent(filePath, header string) (string, error) {
	if filePath == "" || strings.Contains(filePath, header) {
		return filePath, nil
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	content, err := ReadAllFromFile(filePath)
	if err != nil || !strings.Contains(string(content), header) {
		return "", err
	}
	return string(content), nil
}

func LoadCertPair(certFile, keyFile string) (certContent, keyContent string, ok bool) {
	var wg sync.WaitGroup
	var certErr, keyErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		certContent, certErr = GetCertContent(certFile, "CERTIFICATE")
	}()
	go func() {
		defer wg.Done()
		keyContent, keyErr = GetCertContent(keyFile, "PRIVATE")
	}()
	wg.Wait()

	if certErr != nil || keyErr != nil || certContent == "" || keyContent == "" {
		return "", "", false
	}
	return certContent, keyContent, true
}

func LoadCert(certFile, keyFile string) (tls.Certificate, bool) {
	certContent, keyContent, ok := LoadCertPair(certFile, keyFile)
	if ok {
		certificate, err := tls.X509KeyPair([]byte(certContent), []byte(keyContent))
		if err == nil {
			return certificate, true
		}
	}
	return tls.Certificate{}, false
}

func GetCertType(s string) string {
	if s == "" {
		return "empty"
	}
	if strings.Contains(s, "-----BEGIN ") || strings.Contains(s, "\n") {
		return "text"
	}
	if _, err := os.Stat(s); err == nil {
		return "file"
	}
	return "invalid"
}

// FileExists reports whether the named file or directory exists.
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// TestTcpPort Judge whether the TCP port can open normally
func TestTcpPort(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// TestUdpPort Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// BinaryWrite Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = raw.Write(lenBuf[:])
	_, _ = raw.Write(b)
}

// GetWriteStr get seq str
func GetWriteStr(v ...string) []byte {
	sep := CONN_DATA_SEQ
	sepLen := len(sep)
	total := 0
	for _, s := range v {
		total += len(s) + sepLen
	}

	buffer := make([]byte, 0, total)
	for _, s := range v {
		buffer = append(buffer, s...)
		buffer = append(buffer, sep...)
	}
	return buffer
}

// InStrArr inArray str interface
func InStrArr(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// InIntArr inArray int interface
func InIntArr(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
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
			if IsPort(a) && IsPort(b) {
				start, _ := strconv.Atoi(a)
				end, _ := strconv.Atoi(b)
				if end < start {
					start, end = end, start
				}
				for i := start; i <= end; i++ {
					seen[i] = struct{}{}
				}
			}
			continue
		}
		if IsPort(item) {
			port, _ := strconv.Atoi(item)
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
	pi, err := strconv.Atoi(p)
	if err != nil {
		return false
	}
	if pi > 65536 || pi < 1 {
		return false
	}
	return true
}

// FormatAddress if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return "127.0.0.1:" + s
}

func in(target string, strArray []string) bool {
	sort.Strings(strArray)
	index := sort.SearchStrings(strArray, target)
	if index < len(strArray) && strArray[index] == target {
		return true
	}
	return false
}

func IsBlackIp(ipPort, vkey string, blackIpList []string) bool {
	ip := GetIpByAddr(ipPort)
	if in(ip, blackIpList) {
		logs.Warn("IP [%s] is in the blacklist for [%s]", ip, vkey)
		return true
	}
	return false
}

func CopyBuffer(dst io.Writer, src io.Reader, label ...string) (written int64, err error) {
	buf := CopyBuff.Get()
	defer CopyBuff.Put(buf)
	for {
		nr, er := src.Read(buf)
		//if len(pr)>0 && pr[0] && nr > 50 {
		//	logs.Warn(string(buf[:50]))
		//}
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}

// GetLocalUdpAddr send this ip forget to get a local udp port
func GetLocalUdpAddr() (net.Conn, error) {
	tmpConn, err := net.Dial("udp", GetCustomDNS())
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
}

func GetLocalUdp4Addr() (net.Conn, error) {
	tmpConn, err := net.Dial("udp4", IPv4DNS)
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
}

func GetLocalUdp6Addr() (net.Conn, error) {
	tmpConn, err := net.Dial("udp6", IPv6DNS)
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
}

// GetLocalUdp4IP returns the preferred local IPv4 egress IP.
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

// GetLocalUdp6IP returns the preferred local IPv6 egress IP.
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

// ParseStr parse template
func ParseStr(str string) (string, error) {
	tmp := template.New("npc")
	var err error
	w := new(bytes.Buffer)
	if tmp, err = tmp.Parse(str); err != nil {
		return "", err
	}
	if err = tmp.Execute(w, GetEnvMap()); err != nil {
		return "", err
	}
	return w.String(), nil
}

// GetEnvMap get env
func GetEnvMap() map[string]string {
	m := make(map[string]string)
	environ := os.Environ()
	for i := range environ {
		tmp := strings.Split(environ[i], "=")
		if len(tmp) == 2 {
			m[tmp[0]] = tmp[1]
		}
	}
	return m
}

// TrimArr throw the empty element of the string array
func TrimArr(arr []string) []string {
	newArr := make([]string, 0)
	for _, v := range arr {
		trimmed := strings.TrimSpace(v) // 去除前后空白
		if trimmed != "" {
			newArr = append(newArr, trimmed)
		}
	}
	return newArr
}

func IsArrContains(arr []string, val string) bool {
	if arr == nil {
		return false
	}
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// RemoveArrVal remove value from string array
func RemoveArrVal(arr []string, val string) []string {
	for k, v := range arr {
		if v == val {
			arr = append(arr[:k], arr[k+1:]...)
			return arr
		}
	}
	return arr
}

func HandleArrEmptyVal(list []string) []string {
	for len(list) > 0 && (list[len(list)-1] == "" || strings.TrimSpace(list[len(list)-1]) == "") {
		list = list[:len(list)-1]
	}

	for i := 0; i < len(list); i++ {
		list[i] = strings.TrimSpace(list[i])
		if i > 0 && list[i] == "" {
			list[i] = list[i-1]
		}
	}

	return list
}

func ExtendArrs(arrays ...*[]string) int {
	maxLength := 0
	for _, arr := range arrays {
		if len(*arr) > maxLength {
			maxLength = len(*arr)
		}
	}

	if maxLength == 0 {
		return 0
	}

	for _, arr := range arrays {
		for len(*arr) < maxLength {
			if len(*arr) == 0 {
				*arr = append(*arr, "")
			} else {
				*arr = append(*arr, (*arr)[len(*arr)-1])
			}
		}
	}

	return maxLength
}

// BytesToNum convert bytes to num
func BytesToNum(b []byte) int {
	var str string
	for i := 0; i < len(b); i++ {
		str += strconv.Itoa(int(b[i]))
	}
	x, _ := strconv.Atoi(str)
	return x
}

// GetSyncMapLen get the length of the sync map
func GetSyncMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}

func GetExtFromPath(path string) string {
	s := strings.Split(path, ".")
	re, err := regexp.Compile(`(\w+)`)
	if err != nil {
		return ""
	}
	return string(re.Find([]byte(s[0])))
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
	} else {
		tmpConn, err := GetLocalUdp4Addr()
		if err != nil {
			return localAddr, fmt.Errorf("get local ipv4 addr: %w", err)
		}
		ip4 := tmpConn.LocalAddr().(*net.UDPAddr).IP.String()
		return ip4 + ":" + port, nil
	}
}

var externalIp string
var ipApis = []string{
	"https://4.ipw.cn",
	"https://api.ipify.org",
	"http://ipinfo.io/ip",
	"https://api64.ipify.org",
	"https://6.ipw.cn",
	"http://api.ip.sb",
	"http://myexternalip.com/raw",
	"http://ifconfig.me/ip",
	"http://ident.me",
	"https://d-jy.net/ip",
}

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

func GetIntranetIp() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, address := range addrs {
		// 检查 IP 地址判断是否为回环地址
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
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}

func GetOutboundIPv6() net.IP {
	tmpConn, err := GetLocalUdp6Addr()
	if err == nil {
		return tmpConn.LocalAddr().(*net.UDPAddr).IP
	}
	return nil
}

func IsValidIP(ip string) bool {
	parsedIP := net.ParseIP(ip)
	return parsedIP != nil
}

func IsPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := IP.To4(); ip4 != nil {
		switch true {
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
	// Check for IPv6 private addresses
	if ip6 := IP.To16(); ip6 != nil {
		if ip6.IsPrivate() {
			return false
		}
		return true
	}
	return false
}

func GetServerIp(ip string) string {
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

func JoinHostPort(host string, port string) string {
	return net.JoinHostPort(host, port)
}

func RandomBytes(maxLen int) ([]byte, error) {
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(maxLen+1)))
	if err != nil {
		return nil, err
	}
	n := int(nBig.Int64())
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func SetTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	time.Local = loc
	return nil
}

var (
	timeOffset   time.Duration
	ntpServer    string
	syncInterval = 5 * time.Minute
	lastSyncMono time.Time
	timeMutex    sync.RWMutex
	syncCh       = make(chan struct{}, 1)
)

func SetNtpServer(server string) {
	timeMutex.Lock()
	defer timeMutex.Unlock()
	ntpServer = server
}

func SetNtpInterval(d time.Duration) {
	timeMutex.Lock()
	defer timeMutex.Unlock()
	syncInterval = d
}

func CalibrateTimeOffset(server string) (time.Duration, error) {
	if server == "" {
		return 0, nil
	}
	ntpTime, err := ntp.Time(server)
	if err != nil {
		return 0, err
	}
	return time.Until(ntpTime), nil
}

func TimeOffset() time.Duration {
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return timeOffset
}

func TimeNow() time.Time {
	SyncTime()
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return time.Now().Add(timeOffset)
}

func SyncTime() {
	timeMutex.RLock()
	srv, last := ntpServer, lastSyncMono
	interval := syncInterval
	timeMutex.RUnlock()

	if srv == "" || (!last.IsZero() && time.Since(last) < interval) {
		return
	}

	select {
	case syncCh <- struct{}{}:
		defer func() { <-syncCh }()
	default:
		return
	}

	now := time.Now()
	timeMutex.Lock()
	lastSyncMono = now
	timeMutex.Unlock()

	offset, err := CalibrateTimeOffset(srv)
	if err != nil {
		logs.Error("ntp[%s] sync failed: %v", srv, err)
	}

	timeMutex.Lock()
	timeOffset = offset
	timeMutex.Unlock()

	if offset != 0 {
		logs.Info("ntp[%s] offset=%v", srv, offset)
	}
}

// TimestampToBytes 8bit
func TimestampToBytes(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}

// BytesToTimestamp 8bit
func BytesToTimestamp(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}

func ValidatePoW(bits int, parts ...string) bool {
	if bits < 1 || bits > 256 {
		return false
	}

	data := strings.Join(parts, "")
	sum := sha256.Sum256([]byte(data))
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if sum[i] != 0 {
			return false
		}
	}
	remBits := bits % 8
	if remBits > 0 {
		mask := byte(0xFF << (8 - remBits))
		if (sum[fullBytes] & mask) != 0 {
			return false
		}
	}
	return true
}

func IsTrustedProxy(list, ipStr string) bool {
	if list == "" || ipStr == "" {
		return false
	}

	ipStr = strings.TrimSpace(ipStr)

	if h, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = h
	}
	if strings.HasPrefix(ipStr, "[") && strings.HasSuffix(ipStr, "]") {
		ipStr = ipStr[1 : len(ipStr)-1]
	}
	if i := strings.LastIndex(ipStr, "%"); i != -1 { // fe80::1%eth0
		ipStr = ipStr[:i]
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()

	for _, raw := range strings.Split(list, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		if entry == "*" {
			return true
		}

		// CIDR（IPv4/IPv6）
		if strings.Contains(entry, "/") {
			if _, cidr, err := net.ParseCIDR(entry); err == nil && cidr.Contains(ip) {
				return true
			}
			continue
		}

		// if "192.168.*.*"
		if strings.Contains(entry, "*") {
			if ip4 == nil {
				continue
			}
			pSegs := strings.Split(entry, ".")
			if len(pSegs) != 4 {
				continue
			}
			matched := true
			for i := 0; i < 4; i++ {
				if pSegs[i] == "*" {
					continue
				}
				n, err := strconv.Atoi(pSegs[i])
				if err != nil || n < 0 || n > 255 || int(ip4[i]) != n {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
			continue
		}

		if e := net.ParseIP(entry); e != nil && e.Equal(ip) {
			return true
		}
	}

	return false
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
	if strings.HasPrefix(ipStr, "[") && strings.HasSuffix(ipStr, "]") {
		ipStr = ipStr[1 : len(ipStr)-1]
	}
	if i := strings.LastIndex(ipStr, "%"); i != -1 { // fe80::1%eth0
		ipStr = ipStr[:i]
	}
	return net.ParseIP(ipStr)
}

// IsPublicIPStrict checks if IP is globally routable on the Internet.
// It excludes RFC1918, loopback, link-local, multicast, unspecified,
// and also excludes CGNAT / TEST-NET / benchmarking / documentation ranges.
func IsPublicIPStrict(ip net.IP) bool {
	if ip == nil {
		return false
	}
	ip = NormalizeIP(ip)
	if ip == nil {
		return false
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
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

	var bestV4Public bool
	var bestV6Public bool

	for _, a := range addrs {
		ip := ParseIPFromAddr(a)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			pub := IsPublicIPStrict(ip)
			if bestV4 == "" {
				bestV4 = a
				bestV4Public = pub
			} else if !bestV4Public && pub {
				bestV4 = a
				bestV4Public = true
			}
			continue
		}
		pub := IsPublicIPStrict(ip)
		if bestV6 == "" {
			bestV6 = a
			bestV6Public = pub
		} else if !bestV6Public && pub {
			bestV6 = a
			bestV6Public = true
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
// Rule (per your requirement):
// - If both sides contain IPv6 -> return self IPv6 (best, public preferred)
// - Else -> return self IPv4 (best, public preferred)
// - Else -> return self IPv6 if any
// - Else -> return fallback (first valid) or ""
//
// Works when:
// - selfLocal is empty (returns "")
// - selfLocal has 1 address (no comma)
// - selfLocal has many addresses (comma list)
// - peerLocal is empty (IPv6 mutual check fails, will choose IPv4 first)
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

	// already concrete and family matches
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
