package ui

import (
	"github.com/djylb/nps/lib/connprofile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginPageCommonDataMap(t *testing.T) {
	data := LoginPageCommonData{
		WebBaseURL:     "/nps",
		HeadCustomCode: "<meta name=\"x\" content=\"1\">",
		Version:        "1.0.0",
		Year:           2026,
		CaptchaOpen:    true,
		CaptchaHTML:    "<div>captcha</div>",
	}.Map()

	if data["web_base_url"] != "/nps" || data["version"] != "1.0.0" || data["captcha_open"] != true {
		t.Fatalf("LoginPageCommonData.Map() = %#v", data)
	}
	if _, ok := data["captcha_html"]; !ok {
		t.Fatalf("LoginPageCommonData.Map() should include captcha_html, got %#v", data)
	}
}

func TestManagementPageCommonDataMap(t *testing.T) {
	data := ManagementPageCommonData{
		WebBaseURL:     "/nps",
		HeadCustomCode: "<style>.x{}</style>",
		Version:        "1.0.0",
		Year:           2026,
		IsAdmin:        true,
		Username:       "admin",
		BridgeType:     "tcp",
		BridgeAddr:     "127.0.0.1:8024",
		BridgeIP:       "127.0.0.1",
		BridgePort:     "8024",
		WindowsSuffix:  ".exe",
		QUICAddr:       "127.0.0.1:8025/nps",
		ProxyPort:      "8080",
	}.Map()

	if data["bridgeType"] != "tcp" || data["addr"] != "127.0.0.1:8024" || data["proxyPort"] != "8080" {
		t.Fatalf("ManagementPageCommonData.Map() = %#v", data)
	}
	if data["win"] != ".exe" {
		t.Fatalf("ManagementPageCommonData.Map() should include optional fields, got %#v", data)
	}
}

func TestManagementPageServerUpdateOnlyForAdmin(t *testing.T) {
	for _, c := range []struct {
		admin, update, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
	} {
		got := ManagementPageCommonData{IsAdmin: c.admin, ServerUpdate: c.update}.Map()["allow_server_update"]
		if got != c.want {
			t.Errorf("admin=%v update=%v: allow_server_update = %v, want %v", c.admin, c.update, got, c.want)
		}
	}
}

// IPv6 addresses are bracketed so that the start commands built from the
// address and port are valid URLs; WebSocket URLs carry the path, QUIC the
// ALPN.
func TestManagementPageServerURLs(t *testing.T) {
	d := ManagementPageCommonData{
		TCPIP: "2001:db8::1", TCPPort: "8024",
		TLSIP: "[::1]", TLSPort: "8025",
		QUICIP: "fe80::1", QUICPort: "8026", QUICAddr: "[fe80::1]:8026/n ps",
		WSIP: "nps.example.com", WSPort: "80", WSSIP: "nps.example.com", WSSPort: "443", WSPath: "/ws",
	}
	want := map[string]string{
		"tcp":  "tcp://[2001:db8::1]:8024",
		"tls":  "tls://[::1]:8025",
		"quic": "quic://[fe80::1]:8026?alpn=n+ps",
		"ws":   "ws://nps.example.com:80/ws",
		"wss":  "wss://nps.example.com:443/ws",
	}
	got := d.ServerURLs()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ServerURLs()[%s] = %q, want %q", k, got[k], v)
		}
		cands, err := connprofile.Parse(v, "tcp")
		if err != nil || len(cands) != 1 {
			t.Errorf("connprofile.Parse(%q) = %v, %v", v, cands, err)
		}
	}
	if len(got) != len(want) {
		t.Errorf("ServerURLs() = %v", got)
	}
	if _, ok := d.Map()["kcp_ip"]; ok {
		t.Error("kcp_ip is no longer provided")
	}
}

// The point-to-point start command uses the URL of the primary carrier; kcp
// clients connect over QUIC.
func TestManagementPageBridgeURL(t *testing.T) {
	base := ManagementPageCommonData{QUICIP: "10.0.0.1", QUICPort: "8025", WSIP: "h", WSPort: "80", WSPath: "/ws", TCPIP: "10.0.0.1", TCPPort: "8024"}
	for _, c := range []struct{ typ, addr, want string }{
		{"tcp", "10.0.0.1:8024", "tcp://10.0.0.1:8024"},
		{"kcp", "10.0.0.1:8025", "quic://10.0.0.1:8025"},
		{"ws", "h:80", "ws://h:80/ws"},
		{"tls", "10.0.0.1:9", "10.0.0.1:9"},
	} {
		d := base
		d.BridgeType, d.BridgeAddr = c.typ, c.addr
		if got := d.Map()["bridge_url"]; got != c.want {
			t.Errorf("type %s: bridge_url = %v, want %s", c.typ, got, c.want)
		}
	}
}

// The client details assign the shaping text before appending it; a var
// declared after its use is undefined there.
func TestClientListShapedAssignedBeforeUse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "views", "client", "list.html"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	decl, use := strings.Index(src, "var shaped = ''"), strings.Index(src, "transport += shaped")
	if decl < 0 || use < 0 || use < decl {
		t.Fatalf("transport += shaped (at %d) must follow var shaped (at %d)", use, decl)
	}
}
