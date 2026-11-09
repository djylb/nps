package ui

import (
	"github.com/djylb/nps/lib/connprofile"
	"net/url"
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
// address and port are valid links; WebSocket links carry the path, QUIC the
// ALPN. Each link, with a vkey put in the way npcLink (language.js) does it,
// is read back by connprofile.ParseLink.
func TestManagementPageLinkURLs(t *testing.T) {
	d := ManagementPageCommonData{
		TCPIP: "2001:db8::1", TCPPort: "8024",
		TLSIP: "[::1]", TLSPort: "8025",
		QUICIP: "fe80::1", QUICPort: "8026", QUICAddr: "[fe80::1]:8026/n ps",
		WSIP: "nps.example.com", WSPort: "80", WSSIP: "nps.example.com", WSSPort: "443", WSPath: "/w s",
	}
	want := map[string]string{
		"tcp":  "npc://@[2001:db8::1]:8024?type=tcp",
		"tls":  "npc://@[::1]:8025?type=tls",
		"quic": "npc://@[fe80::1]:8026?type=quic&alpn=n+ps",
		"ws":   "npc://@nps.example.com:80/w%20s?type=ws",
		"wss":  "npc://@nps.example.com:443/w%20s?type=wss",
	}
	got := d.LinkURLs()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("LinkURLs()[%s] = %q, want %q", k, got[k], v)
		}
		vkey := "k;1:/ @"
		l, err := connprofile.ParseLink(strings.Replace(v, "npc://@", "npc://"+strings.ReplaceAll(url.QueryEscape(vkey), "+", "%20")+"@", 1))
		if err != nil || l.VKey != vkey {
			t.Errorf("ParseLink(%q) = %+v, %v", v, l, err)
			continue
		}
		cands, err := connprofile.Parse(l.Server, "")
		if err != nil || len(cands) != 1 || cands[0].Carrier != k {
			t.Errorf("connprofile.Parse(%q) = %v, %v", l.Server, cands, err)
		}
	}
	if len(got) != len(want) {
		t.Errorf("LinkURLs() = %v", got)
	}
	if _, ok := d.Map()["kcp_ip"]; ok {
		t.Error("kcp_ip is no longer provided")
	}
}

// The point-to-point start command uses the link of the primary carrier; kcp
// clients connect over QUIC.
func TestManagementPageBridgeURL(t *testing.T) {
	base := ManagementPageCommonData{QUICIP: "10.0.0.1", QUICPort: "8025", WSIP: "h", WSPort: "80", WSPath: "/ws", TCPIP: "10.0.0.1", TCPPort: "8024"}
	for _, c := range []struct{ typ, addr, want string }{
		{"tcp", "10.0.0.1:8024", "npc://@10.0.0.1:8024?type=tcp"},
		{"kcp", "10.0.0.1:8025", "npc://@10.0.0.1:8025?type=quic"},
		{"ws", "h:80", "npc://@h:80/ws?type=ws"},
		{"tls", "10.0.0.1:9", "npc://@10.0.0.1:9?type=tls"},
		{"wss", "h:443/p", "npc://@h:443/p?type=wss"},
	} {
		d := base
		d.BridgeType, d.BridgeAddr = c.typ, c.addr
		got, _ := d.Map()["bridge_url"].(string)
		if got != c.want {
			t.Errorf("type %s: bridge_url = %v, want %s", c.typ, got, c.want)
		}
		if _, err := connprofile.ParseLink(strings.Replace(got, "npc://@", "npc://k@", 1)); err != nil {
			t.Errorf("type %s: %v", c.typ, err)
		}
	}
}

// The start commands of the pages are launch links in double quotes.
func TestStartCommandsUseLaunchLinks(t *testing.T) {
	for file, want := range map[string][]string{
		"client/list.html": {`{{.npc_bin}} -launch=\"`, `{{.npc_bin}} install -launch=\"`, `npcLink(`},
		"index/list.html":  {`{{.npc_bin}} -launch=\"`, `-L=\"`, `encodeURIComponent(row.Password)`},
	} {
		b, err := os.ReadFile(filepath.Join("..", "views", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s lacks %s", file, w)
			}
		}
		if strings.Contains(string(b), "-vkey=") || strings.Contains(string(b), "-server=") {
			t.Errorf("%s still writes a -server/-vkey command", file)
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

// The pages that write start commands execute with the page data, also with
// no carrier enabled, and carry the link of an enabled one.
func TestStartCommandPagesExecute(t *testing.T) {
	for _, d := range []ManagementPageCommonData{{}, {TLSIP: "h", TLSPort: "8025", WindowsSuffix: ".exe", BridgeType: "tls", BridgeAddr: "h:8025"}} {
		for _, f := range []string{"client/list.html", "index/list.html"} {
			out, err := execute(filepath.Join("..", "views", f), d.Map())
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if d.TLSPort != "" && !strings.Contains(out, "npc://@h:8025?type=tls") {
				t.Errorf("%s has no tls link", f)
			}
			if d.WindowsSuffix != "" && !strings.Contains(out, "npc.exe") {
				t.Errorf("%s has no npc.exe command", f)
			}
		}
	}
}
