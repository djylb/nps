package config

import (
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
