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
	if net.ParseIP(s) != nil {
		return false
	}
	return true
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
		return fmt.Sprintf("[%s]:%s", host, port)
	}
	return fmt.Sprintf("%s:%s", host, port)
}

func SplitServerAndPath(s string) (server, path string) {
	index := strings.Index(s, "/")
	if index == -1 {
		return s, ""
	}
	return s[:index], s[index:]
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
	var match bool
	IsLine := "^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\\-]{0,61}[a-zA-Z0-9])?\\.)+[a-zA-Z]{2,6}(/)"
	NotLine := "^((http://)|(https://))?([a-zA-Z0-9]([a-zA-Z0-9\\-]{0,61}[a-zA-Z0-9])?\\.)+[a-zA-Z]{2,6}"
	match, _ = regexp.MatchString(IsLine, domain)
	if !match {
		match, _ = regexp.MatchString(NotLine, domain)
	}
	return match
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
	noAccountMap := accountMap == nil || len(accountMap) == 0
	noAuthMap := authMap == nil || len(authMap) == 0
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
	if user == "" && passwd == "" && (accountMap == nil || len(accountMap) == 0) && (authMap == nil || len(authMap) == 0) {
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
	defer f.Close()
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
	if err != nil {
		return false
	}
	return true
}

// TestUdpPort Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	if err != nil {
		return false
	}
	return true
}

// BinaryWrite Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	_ = binary.Write(raw, binary.LittleEndian, int32(len(b)))
	_ = binary.Write(raw, binary.LittleEndian, b)
}

// GetWriteStr get seq str
func GetWriteStr(v ...string) []byte {
	buffer := new(bytes.Buffer)
	var l int32
	for _, v := range v {
		l += int32(len([]byte(v))) + int32(len([]byte(CONN_DATA_SEQ)))
		_ = binary.Write(buffer, binary.LittleEndian, []byte(v))
		_ = binary.Write(buffer, binary.LittleEndian, []byte(CONN_DATA_SEQ))
	}
	return buffer.Bytes()
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
	tmpConn, err := net.Dial("udp4", "8.8.8.8:53")
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
}

func GetLocalUdp6Addr() (net.Conn, error) {
	tmpConn, err := net.Dial("udp6", "[2400:3200::1]:53")
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
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

func IsSameIPType(addr1, addr2 string) bool {
	ip1 := strings.Contains(addr1, "[")
	ip2 := strings.Contains(addr2, "[")

	if ip1 == ip2 {
		return true
	}
	return false
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
		return fmt.Sprintf("[%s]:%s", ip6, port), nil
	} else {
		tmpConn, err := GetLocalUdp4Addr()
		if err != nil {
			return localAddr, fmt.Errorf("get local ipv4 addr: %w", err)
		}
		ip4 := tmpConn.LocalAddr().(*net.UDPAddr).IP.String()
		return fmt.Sprintf("%s:%s", ip4, port), nil
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
	defer conn.Close()

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
	return ntpTime.Sub(time.Now()), nil
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
