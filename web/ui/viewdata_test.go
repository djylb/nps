package ui

import (
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
	if data["win"] != ".exe" || data["quic_query"] != "?alpn=nps" {
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
// address and port are valid URLs.
func TestManagementPageIPv6Hosts(t *testing.T) {
	data := ManagementPageCommonData{TCPIP: "2001:db8::1", TLSIP: "[::1]", WSIP: "nps.example.com", QUICIP: "fe80::1"}.Map()
	for k, want := range map[string]string{"tcp_ip": "[2001:db8::1]", "tls_ip": "[::1]", "ws_ip": "nps.example.com", "quic_ip": "[fe80::1]"} {
		if data[k] != want {
			t.Errorf("%s = %v, want %s", k, data[k], want)
		}
	}
	if _, ok := data["kcp_ip"]; ok {
		t.Error("kcp_ip is no longer provided")
	}
}
