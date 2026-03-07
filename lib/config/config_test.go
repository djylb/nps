package config

import (
	"regexp"
	"testing"
)

func TestGetAllTitle_DuplicateRejected(t *testing.T) {
	_, err := getAllTitle("[common]\na=1\n[common]\nb=2\n")
	if err == nil {
		t.Fatalf("expected duplicate title to return an error")
	}
}

func TestGetAllTitle_ParseAll(t *testing.T) {
	titles, err := getAllTitle("[common]\na=1\n[web]\nhost=a.com\n[tcp]\nmode=tcp\n")
	if err != nil {
		t.Fatalf("getAllTitle returned error: %v", err)
	}
	if len(titles) != 3 {
		t.Fatalf("expected 3 titles, got %d", len(titles))
	}
	if titles[0] != "[common]" || titles[1] != "[web]" || titles[2] != "[tcp]" {
		t.Fatalf("unexpected title order/content: %#v", titles)
	}
}

func TestReg(t *testing.T) {
	content := `
[common]
server=127.0.0.1:8284
tp=tcp
vkey=123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile=122123
header_user-Agent=122123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile="122123"
header_user-Agent=122123
[tunnel1]
type=udp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
[tunnel2]
type=tcp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
`
	re, err := regexp.Compile(`\[.+?\]`)
	if err != nil {
		t.Fatalf("compile regexp failed: %v", err)
	}
	all := re.FindAllString(content, -1)
	if len(all) != 5 {
		t.Fatalf("unexpected title count: %d", len(all))
	}
}

func TestDealCommon(t *testing.T) {
	s := `server_addr=127.0.0.1:8284
conn_type=kcp
vkey=123
auto_reconnection=false
basic_username=admin
basic_password=pass
compress=false
crypt=false
web_username=user
web_password=web-pass
rate_limit=1024
flow_limit=2048
max_conn=12
disconnect_timeout=30
tls_enable=true`

	c := dealCommon(s)
	if c.Server != "127.0.0.1:8284" || c.Tp != "kcp" || c.VKey != "123" {
		t.Fatalf("basic common fields parse failed: %+v", c)
	}
	if c.AutoReconnection {
		t.Fatalf("auto_reconnection should be false")
	}
	if c.Client == nil || c.Client.Cnf == nil {
		t.Fatalf("client or client config not initialized")
	}
	if c.Client.Cnf.U != "admin" || c.Client.Cnf.P != "pass" {
		t.Fatalf("basic auth parse failed: %+v", c.Client.Cnf)
	}
	if c.Client.WebUserName != "user" || c.Client.WebPassword != "web-pass" {
		t.Fatalf("web auth parse failed: %+v", c.Client)
	}
	if c.Client.RateLimit != 1024 || c.Client.Flow.FlowLimit != 2048 || c.Client.MaxConn != 12 {
		t.Fatalf("limit fields parse failed: %+v", c.Client)
	}
	if c.DisconnectTime != 30 || !c.TlsEnable {
		t.Fatalf("disconnect or tls parse failed: %+v", c)
	}
}

func TestDealCommon_Defaults(t *testing.T) {
	c := dealCommon(`vkey=abc`)
	if c.Tp != "tcp" {
		t.Fatalf("unexpected default tp: %s", c.Tp)
	}
	if !c.AutoReconnection {
		t.Fatalf("unexpected default auto_reconnection: %v", c.AutoReconnection)
	}
	if c.Client == nil || c.Client.Cnf == nil {
		t.Fatalf("client defaults are not initialized")
	}
}

func TestGetTitleContent(t *testing.T) {
	s := "[common]"
	if getTitleContent(s) != "common" {
		t.Fail()
	}
}

func TestStripCommentLines(t *testing.T) {
	content := "a=1\n#comment\n  #comment2\nb=2\n"
	cleaned := stripCommentLines(content)
	if cleaned != "a=1\nb=2\n" {
		t.Fatalf("unexpected cleaned config: %q", cleaned)
	}
}

func TestDealHost_HeaderAndResponseMapping(t *testing.T) {
	h := dealHost(`host=example.com
target_addr=127.0.0.1:8080,127.0.0.1:8081
proxy_protocol=2
auto_cors=true
header_X-Test=test-value
response_Cache-Control=no-cache`)

	if h.Host != "example.com" {
		t.Fatalf("unexpected host: %s", h.Host)
	}
	if h.Target.TargetStr != "127.0.0.1:8080\n127.0.0.1:8081" {
		t.Fatalf("unexpected target addresses: %q", h.Target.TargetStr)
	}
	if h.Target.ProxyProtocol != 2 {
		t.Fatalf("unexpected proxy protocol: %d", h.Target.ProxyProtocol)
	}
	if !h.AutoCORS {
		t.Fatalf("auto_cors not parsed")
	}
	if h.HeaderChange != "X-Test:test-value\n" {
		t.Fatalf("unexpected header mapping: %q", h.HeaderChange)
	}
	if h.RespHeaderChange != "Cache-Control:no-cache\n" {
		t.Fatalf("unexpected response header mapping: %q", h.RespHeaderChange)
	}
}

func TestDealTunnel_ParseFlagsAndRules(t *testing.T) {
	tnl := dealTunnel(`server_port=10080
server_ip=0.0.0.0
mode=http
target_addr=127.0.0.1:80,127.0.0.1:8080
proxy_protocol=1
password=pwd
socks5_proxy=true
http_proxy=false
dest_acl_mode=2
dest_acl_rules=10.0.0.0/8,192.168.0.0/16
local_path=/tmp
strip_pre=/api
read_only=true`)

	if tnl.Ports != "10080" || tnl.ServerIp != "0.0.0.0" || tnl.Mode != "http" {
		t.Fatalf("basic tunnel fields parse failed: %+v", tnl)
	}
	if tnl.Target.TargetStr != "127.0.0.1:80\n127.0.0.1:8080" {
		t.Fatalf("unexpected target addresses: %q", tnl.Target.TargetStr)
	}
	if !tnl.Socks5Proxy || tnl.HttpProxy {
		t.Fatalf("proxy flags parse failed: socks5=%v http=%v", tnl.Socks5Proxy, tnl.HttpProxy)
	}
	if tnl.DestAclMode != 2 || tnl.DestAclRules != "10.0.0.0/8\n192.168.0.0/16" {
		t.Fatalf("dest acl parse failed: mode=%d rules=%q", tnl.DestAclMode, tnl.DestAclRules)
	}
	if tnl.LocalPath != "/tmp" || tnl.StripPre != "/api" || !tnl.ReadOnly {
		t.Fatalf("path/read-only parse failed: %+v", tnl)
	}
}

func TestDealMultiUser_IgnoreInvalidAndComments(t *testing.T) {
	accounts := dealMultiUser("\n#comment\nalice = a1\nbob=b2\ncharlie\n =drop\n")

	if len(accounts) != 3 {
		t.Fatalf("unexpected account count: %#v", accounts)
	}
	if accounts["alice"] != "a1" || accounts["bob"] != "b2" || accounts["charlie"] != "" {
		t.Fatalf("unexpected account map: %#v", accounts)
	}
	if _, ok := accounts[""]; ok {
		t.Fatalf("empty key should be ignored: %#v", accounts)
	}
}

func TestDelLocalService_ParseAllFields(t *testing.T) {
	local := delLocalService(`local_port=9000
local_type=tcp
local_ip=127.0.0.1
password=secret
target_addr=10.0.0.1:22
target_type=tcp
local_proxy=true
fallback_secret=true`)

	if local.Port != 9000 || local.Type != "tcp" || local.Ip != "127.0.0.1" {
		t.Fatalf("basic local fields parse failed: %+v", local)
	}
	if local.Password != "secret" || local.Target != "10.0.0.1:22" || local.TargetType != "tcp" {
		t.Fatalf("target/auth parse failed: %+v", local)
	}
	if !local.LocalProxy || !local.Fallback {
		t.Fatalf("boolean flags parse failed: %+v", local)
	}
}
