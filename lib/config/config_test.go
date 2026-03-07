package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

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

func TestDealHostAndHealth(t *testing.T) {
	h := dealHost(`host=example.com
target_addr=127.0.0.1:8080,127.0.0.1:8081
proxy_protocol=1
host_change=backend.internal
scheme=https
location=/api
path_rewrite=/v1
https_just_proxy=true
auto_ssl=true
auto_https=true
auto_cors=true
compat_mode=true
redirect_url=https://redirect
target_is_https=true
header_x-trace=trace
response_x-server=nps`)

	if h.Host != "example.com" || h.Scheme != "https" {
		t.Fatalf("host basic fields parse failed: %+v", h)
	}
	if h.Target.TargetStr != "127.0.0.1:8080\n127.0.0.1:8081" || h.Target.ProxyProtocol != 1 {
		t.Fatalf("host target parse failed: %+v", h.Target)
	}
	if h.HostChange != "backend.internal" || h.Location != "/api" || h.PathRewrite != "/v1" {
		t.Fatalf("host routing parse failed: %+v", h)
	}
	if !h.HttpsJustProxy || !h.AutoSSL || !h.AutoHttps || !h.AutoCORS || !h.CompatMode || !h.TargetIsHttps {
		t.Fatalf("host bool fields parse failed: %+v", h)
	}
	if h.HeaderChange != "x-trace:trace\n" || h.RespHeaderChange != "x-server:nps\n" {
		t.Fatalf("header change parse failed: header=%q response=%q", h.HeaderChange, h.RespHeaderChange)
	}

	health := dealHealth(`health_check_timeout=10
health_check_max_failed=3
health_check_interval=5
health_http_url=/healthz
health_check_type=http
health_check_target=127.0.0.1:8080`)

	if health.HealthCheckTimeout != 10 || health.HealthMaxFail != 3 || health.HealthCheckInterval != 5 {
		t.Fatalf("health number fields parse failed: %+v", health)
	}
	if health.HttpHealthUrl != "/healthz" || health.HealthCheckType != "http" || health.HealthCheckTarget != "127.0.0.1:8080" {
		t.Fatalf("health string fields parse failed: %+v", health)
	}
}

func TestDealTunnelAndLocalService(t *testing.T) {
	tunnel := dealTunnel(`server_port=9000-9001
server_ip=0.0.0.0
mode=tcp
target_addr=127.0.0.1:9002,127.0.0.1:9003
proxy_protocol=2
target_port=10000
target_ip=127.0.0.1
password=pass
socks5_proxy=true
http_proxy=true
dest_acl_mode=1
dest_acl_rules=127.0.0.1:80,10.0.0.0/8:* 
local_path=/tmp
strip_pre=/api
read_only=true`)

	if tunnel.Ports != "9000-9001" || tunnel.ServerIp != "0.0.0.0" || tunnel.Mode != "tcp" {
		t.Fatalf("tunnel basic fields parse failed: %+v", tunnel)
	}
	if tunnel.Target.TargetStr != "10000" || tunnel.TargetAddr != "127.0.0.1" {
		t.Fatalf("tunnel target parse failed: %+v", tunnel)
	}
	if !tunnel.Socks5Proxy || !tunnel.HttpProxy || !tunnel.ReadOnly {
		t.Fatalf("tunnel bool fields parse failed: %+v", tunnel)
	}
	if tunnel.DestAclMode != 1 || tunnel.DestAclRules != "127.0.0.1:80\n10.0.0.0/8:* " {
		t.Fatalf("tunnel acl parse failed: %+v", tunnel)
	}

	local := delLocalService(`local_port=1080
local_type=socks5
local_ip=127.0.0.1
password=123
target_addr=127.0.0.1:8080
target_type=tcp
local_proxy=true
fallback_secret=true`)
	if local.Port != 1080 || local.Type != "socks5" || local.Ip != "127.0.0.1" {
		t.Fatalf("local service basic fields parse failed: %+v", local)
	}
	if local.Password != "123" || local.Target != "127.0.0.1:8080" || local.TargetType != "tcp" {
		t.Fatalf("local service route fields parse failed: %+v", local)
	}
	if !local.LocalProxy || !local.Fallback {
		t.Fatalf("local service bool fields parse failed: %+v", local)
	}
}

func TestDealMultiUserAndGetAllTitle(t *testing.T) {
	users := dealMultiUser("#comment\nuser1=pass1\n user2 = pass2 \nuser3\n")
	if len(users) != 3 {
		t.Fatalf("unexpected user count: %d", len(users))
	}
	if users["user1"] != "pass1" || users["user2"] != "pass2" || users["user3"] != "" {
		t.Fatalf("unexpected users map: %+v", users)
	}

	titles, err := getAllTitle("[common]\n[test]\n")
	if err != nil {
		t.Fatalf("getAllTitle should pass, err=%v", err)
	}
	if len(titles) != 2 || titles[0] != "[common]" || titles[1] != "[test]" {
		t.Fatalf("unexpected titles: %+v", titles)
	}

	if _, err = getAllTitle("[common]\n[common]\n"); err == nil {
		t.Fatalf("duplicate title should return error")
	}
}

func TestNewConfig_ParseSections(t *testing.T) {
	dir := t.TempDir()
	multiUserPath := filepath.Join(dir, "multi_user.ini")
	if err := os.WriteFile(multiUserPath, []byte("u1=p1\nu2=p2\n"), 0o644); err != nil {
		t.Fatalf("write multi user file failed: %v", err)
	}

	content := `[common]
server_addr=127.0.0.1:8284
vkey=test-key
conn_type=tcp

[host-sec]
host=example.com
target_addr=127.0.0.1:8080
multi_account=` + multiUserPath + `

[tunnel-sec]
mode=tcp
server_port=9000
target_addr=127.0.0.1:9001

[health-check]
health_check_timeout=10
health_check_type=tcp

[secret-demo]
local_port=10080
target_addr=127.0.0.1:1080

[p2p-demo]
target_addr=127.0.0.1:10000
`

	configPath := filepath.Join(dir, "nps.conf")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	c, err := NewConfig(configPath)
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	if c.CommonConfig == nil || c.CommonConfig.Server != "127.0.0.1:8284" || c.CommonConfig.VKey != "test-key" {
		t.Fatalf("common section parse failed: %+v", c.CommonConfig)
	}
	if len(c.Hosts) != 1 || c.Hosts[0].Remark != "host-sec" || c.Hosts[0].MultiAccount.AccountMap["u1"] != "p1" {
		t.Fatalf("host section parse failed: %+v", c.Hosts)
	}
	if len(c.Tasks) != 1 || c.Tasks[0].Remark != "tunnel-sec" || c.Tasks[0].Mode != "tcp" {
		t.Fatalf("tunnel section parse failed: %+v", c.Tasks)
	}
	if len(c.Healths) != 1 || c.Healths[0].HealthCheckTimeout != 10 {
		t.Fatalf("health section parse failed: %+v", c.Healths)
	}
	if len(c.LocalServer) != 2 {
		t.Fatalf("local service section parse failed, len=%d", len(c.LocalServer))
	}
	if c.LocalServer[0].Type != "secret" || c.LocalServer[1].Type != "p2p" {
		t.Fatalf("special section parse failed: %+v", c.LocalServer)
	}
}
