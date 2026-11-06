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
	if data["win"] != ".exe" || data["quic_addr"] != "127.0.0.1:8025/nps" {
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
