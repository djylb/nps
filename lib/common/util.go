package common

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/araddon/dateparse"
	"github.com/beego/beego"
	"github.com/beego/beego/logs"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/version"
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

// Get the corresponding IP address through domain name
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

// Check the legality of domain
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

// CheckAuthWithAccountMap
// u current login user
// p current login passwd
// user global user
// passwd global passwd
// accountMap enable multi user auth
func CheckAuthWithAccountMap(u, p, user, passwd string, accountMap map[string]string) bool {
	// Single account check
	if accountMap == nil || len(accountMap) == 0 {
		return u == user && p == passwd
	}

	// Multi-account authentication check
	if len(u) == 0 {
		return false
	}

	if u == user && p == passwd {
		return true
	}

	if P, ok := accountMap[u]; ok && p == P {
		return true
	}

	return false
}

// Check if the Request request is validated
func CheckAuth(r *http.Request, user, passwd string, accountMap map[string]string) bool {
	// Bypass authentication only if user, passwd are empty and multiAccount is nil or empty
	if user == "" && passwd == "" && (accountMap == nil || len(accountMap) == 0) {
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

	return CheckAuthWithAccountMap(pair[0], pair[1], user, passwd, accountMap)
}

// get bool by str
func GetBoolByStr(s string) bool {
	switch s {
	case "1", "true":
		return true
	}
	return false
}

// get str by bool
func GetStrByBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// time
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

// Get verify value
func Getverifyval(vkey string) string {
	return crypt.Md5(vkey)
}

// Change headers and host of request
func ChangeHostAndHeader(r *http.Request, host string, header string, httpOnly bool) {
	// 设置 Host 头部信息
	if host != "" {
		r.Host = host
		if orig := r.Header.Get("Origin"); orig != "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			r.Header.Set("Origin", scheme+"://"+host)
		}
	}

	// 获取请求的客户端 IP
	clientIP := GetIpByAddr(r.RemoteAddr)

	//logs.Debug("get X-Remote-IP = " + clientIP)

	// 获取 X-Forwarded-For 头部的先前值
	xfwdFor := clientIP
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		xfwdFor = strings.Join(prior, ", ") + ", " + clientIP
	}

	//logs.Debug("get X-Forwarded-For = " + xfwdFor)

	// 判断是否需要添加真实 IP 信息
	var addOrigin bool
	if !httpOnly {
		addOrigin, _ = beego.AppConfig.Bool("http_add_origin_header")
		r.Header.Set("X-Forwarded-For", xfwdFor)
	} else {
		addOrigin = false
	}

	// 添加 X-Forwarded-For 和 X-Real-IP 头部信息
	if addOrigin {
		r.Header.Set("X-Forwarded-For", clientIP)
		r.Header.Set("X-Real-IP", clientIP)
	}

	// 设置自定义头部信息
	if header != "" {
		h := strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n")
		for _, v := range h {
			hd := strings.SplitN(v, ":", 2)
			if len(hd) == 2 {
				r.Header.Set(strings.TrimSpace(hd[0]), strings.TrimSpace(hd[1]))
			}
		}
	}
}

// Read file content by file path
func ReadAllFromFile(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ioutil.ReadAll(f)
}

// getCertContent 从文件读取证书/密钥文本
func GetCertContent(filePath, header string) (string, error) {
	if filePath == "" || strings.Contains(filePath, header) {
		return filePath, nil
	}
	content, err := ReadAllFromFile(filePath)
	if err != nil || !strings.Contains(string(content), header) {
		return "", err
	}
	return string(content), nil
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

// Judge whether the TCP port can open normally
func TestTcpPort(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{net.ParseIP("0.0.0.0"), port, ""})
	defer func() {
		if l != nil {
			l.Close()
		}
	}()
	if err != nil {
		return false
	}
	return true
}

// Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{net.ParseIP("0.0.0.0"), port, ""})
	defer func() {
		if l != nil {
			l.Close()
		}
	}()
	if err != nil {
		return false
	}
	return true
}

// Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	binary.Write(raw, binary.LittleEndian, int32(len(b)))
	binary.Write(raw, binary.LittleEndian, b)
}

// get seq str
func GetWriteStr(v ...string) []byte {
	buffer := new(bytes.Buffer)
	var l int32
	for _, v := range v {
		l += int32(len([]byte(v))) + int32(len([]byte(CONN_DATA_SEQ)))
		binary.Write(buffer, binary.LittleEndian, []byte(v))
		binary.Write(buffer, binary.LittleEndian, []byte(CONN_DATA_SEQ))
	}
	return buffer.Bytes()
}

// inArray str interface
func InStrArr(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// inArray int interface
func InIntArr(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// format ports str to a int array
func GetPorts(p string) []int {
	var ps []int
	arr := strings.Split(p, ",")
	for _, v := range arr {
		fw := strings.Split(v, "-")
		if len(fw) == 2 {
			if IsPort(fw[0]) && IsPort(fw[1]) {
				start, _ := strconv.Atoi(fw[0])
				end, _ := strconv.Atoi(fw[1])
				for i := start; i <= end; i++ {
					ps = append(ps, i)
				}
			} else {
				continue
			}
		} else if IsPort(v) {
			p, _ := strconv.Atoi(v)
			ps = append(ps, p)
		}
	}
	return ps
}

// is the string a port
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

// if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return "127.0.0.1:" + s
}

func in(target string, str_array []string) bool {
	sort.Strings(str_array)
	index := sort.SearchStrings(str_array, target)
	if index < len(str_array) && str_array[index] == target {
		return true
	}
	return false
}

// 判断访问地址是否在黑名单内
func IsBlackIp(ipPort, vkey string, blackIpList []string) bool {
	ip := GetIpByAddr(ipPort)
	if in(ip, blackIpList) {
		logs.Error("IP地址[" + ip + "]在隧道[" + vkey + "]黑名单列表内")
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

// send this ip forget to get a local udp port
func GetLocalUdpAddr() (net.Conn, error) {
	tmpConn, err := net.Dial("udp", "114.114.114.114:53")
	if err != nil {
		return nil, err
	}
	return tmpConn, tmpConn.Close()
}

// parse template
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

// get env
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

// throw the empty element of the string array
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

// remove value from string array
func RemoveArrVal(arr []string, val string) []string {
	for k, v := range arr {
		if v == val {
			arr = append(arr[:k], arr[k+1:]...)
			return arr
		}
	}
	return arr
}

// convert bytes to num
func BytesToNum(b []byte) int {
	var str string
	for i := 0; i < len(b); i++ {
		str += strconv.Itoa(int(b[i]))
	}
	x, _ := strconv.Atoi(str)
	return int(x)
}

// get the length of the sync map
func GeSynctMapLen(m sync.Map) int {
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

var externalIp string

func GetExternalIp() string {
	if externalIp != "" {
		return externalIp
	}
	resp, err := http.Get("http://myexternalip.com/raw")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	content, _ := ioutil.ReadAll(resp.Body)
	externalIp = string(content)
	return externalIp
}

func GetIntranetIp() (error, string) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, ""
	}
	for _, address := range addrs {
		// 检查 IP 地址判断是否为回环地址
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil || ipnet.IP.To16() != nil {
				return nil, ipnet.IP.String()
			}
		}
	}
	return errors.New("get intranet ip error"), ""
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

func GetServerIpByClientIp(clientIp net.IP) string {
	if IsPublicIP(clientIp) {
		return GetExternalIp()
	}
	_, ip := GetIntranetIp()
	return ip
}

func PrintVersion() {
	fmt.Printf("Version: %s\nCore version: %s\nSame core version of client and server can connect each other\n", version.VERSION, version.GetVersion())
}
