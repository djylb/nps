package ui

import (
	"html/template"
	"net"
	"net/url"
	"strings"
)

type LoginPageCommonData struct {
	WebBaseURL     string
	HeadCustomCode template.HTML
	Version        string
	Year           int
	CaptchaOpen    bool
	CaptchaHTML    template.HTML
}

func (d LoginPageCommonData) Map() map[string]any {
	data := map[string]any{
		"web_base_url":     d.WebBaseURL,
		"head_custom_code": d.HeadCustomCode,
		"version":          d.Version,
		"year":             d.Year,
		"captcha_open":     d.CaptchaOpen,
	}
	if d.CaptchaHTML != "" {
		data["captcha_html"] = d.CaptchaHTML
	}
	return data
}

type ManagementPageCommonData struct {
	WebBaseURL     string
	HeadCustomCode template.HTML
	Version        string
	Year           int
	IsAdmin        bool
	// AdmitKey is the admission key of the exchange carrier, as hex, shown to
	// administrators only.
	AdmitKey string
	// MixIP and MixPort are the mixed port, where a type=auto link connects;
	// the port is "" when there is none.
	MixIP, MixPort string
	// ServerUpdate tells whether the server can be updated from the web.
	ServerUpdate bool
	Username     string
	BridgeType   string
	// VisitorVKey, when set, is the vkey the access commands show.
	VisitorVKey   string
	BridgeAddr    string
	BridgeIP      string
	BridgePort    string
	WindowsSuffix string
	TCPIP         string
	TCPPort       string
	KCPPort       string
	TLSIP         string
	TLSPort       string
	QUICIP        string
	QUICPort      string
	QUICALPN      string
	QUICAddr      string
	WSPath        string
	WSIP          string
	WSPort        string
	WSSIP         string
	WSSPort       string
	ProxyPort     string
	// Features are the feature switches the templates test, by name.
	Features map[string]bool
}

func (d ManagementPageCommonData) Map() map[string]any {
	data := map[string]any{
		"web_base_url":        d.WebBaseURL,
		"head_custom_code":    d.HeadCustomCode,
		"version":             d.Version,
		"year":                d.Year,
		"isAdmin":             d.IsAdmin,
		"allow_server_update": d.IsAdmin && d.ServerUpdate,
		"username":            d.Username,
		"bridgeType":          d.BridgeType,
		"addr":                d.BridgeAddr,
		"visitor_vkey":        d.VisitorVKey,
		"ip":                  d.BridgeIP,
		"p":                   d.BridgePort,
		"proxyPort":           d.ProxyPort,
	}
	for name, on := range d.Features {
		data[name] = on
	}
	if d.WindowsSuffix != "" {
		data["win"] = d.WindowsSuffix
	}
	// ./npc runs in a shell of Linux and PowerShell; cmd.exe has no ./
	data["npc_bin"] = "./npc"
	if d.WindowsSuffix != "" {
		data["npc_bin"] = "npc" + d.WindowsSuffix
	}
	if d.TCPPort != "" {
		data["tcp_p"] = d.TCPPort
	}
	if d.KCPPort != "" {
		data["kcp_p"] = d.KCPPort
	}
	if d.TLSPort != "" {
		data["tls_p"] = d.TLSPort
	}
	if d.QUICPort != "" {
		data["quic_p"] = d.QUICPort
	}
	if d.QUICALPN != "" {
		data["quic_alpn"] = d.QUICALPN
	}
	if d.WSPath != "" {
		data["ws_path"] = d.WSPath
	}
	if d.WSPort != "" {
		data["ws_p"] = d.WSPort
	}
	if d.WSSPort != "" {
		data["wss_p"] = d.WSSPort
	}
	urls := d.LinkURLs()
	data["link_urls"] = urls
	data["bridge_url"] = d.bridgeURL(urls)
	return data
}

// LinkURLs are the launch links of the enabled bridge carriers, by type
// (auto where there is a mixed port, tcp, tls, quic, ws, wss, http, https): the one place the start commands take them from.
// A link has no vkey yet, "npc://@host:port", for the page to put in with
// npcLink (language.js); an HTTP link carries the path, a QUIC link the
// ALPN.
func (d ManagementPageCommonData) LinkURLs() map[string]string {
	urls := map[string]string{}
	add := func(typ, host, port, path, query string) {
		if port != "" {
			urls[typ] = linkURL(typ, net.JoinHostPort(strings.Trim(host, "[]"), port), path, query)
		}
	}
	add("tcp", d.TCPIP, d.TCPPort, "", "")
	add("tls", d.TLSIP, d.TLSPort, "", "")
	add("quic", d.QUICIP, d.QUICPort, "", d.quicQuery())
	add("ws", d.WSIP, d.WSPort, d.WSPath, withBinary(""))
	add("wss", d.WSSIP, d.WSSPort, d.WSPath, withBinary(""))
	autoQuery := d.admitQuery()
	if d.WSPath != "" && d.WSPath != "/" {
		autoQuery = withBinary(autoQuery)
	}
	add("auto", d.MixIP, d.MixPort, d.WSPath, autoQuery)
	add("http", d.WSIP, d.WSPort, d.WSPath, withBinary(d.admitQuery()))
	add("https", d.WSSIP, d.WSSPort, d.WSPath, withBinary(d.admitQuery()))
	return urls
}

// withBinary appends the subprotocol of the core protocol to the parameters
// of a link to the shared bridge path: a URL candidate sends none by default,
// and on that path none is the old protocol. An auto link only carries it with
// a path, as only wss and https use it.
func withBinary(query string) string {
	if query == "" {
		return "subprotocol=binary"
	}
	return query + "&subprotocol=binary"
}

// admitQuery is the admission key as a link parameter, "" without one.
func (d ManagementPageCommonData) admitQuery() string {
	if d.AdmitKey == "" {
		return ""
	}
	return "admit_key=" + d.AdmitKey
}

// linkURL writes a launch link without its vkey. addr is host:port.
func linkURL(typ, addr, path, query string) string {
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Path: path}
	q := "type=" + typ
	if query != "" {
		q += "&" + query
	}
	return "npc://@" + addr + u.EscapedPath() + "?" + q
}

// quicQuery is the ALPN of the display address "host:port/alpn" as a link parameter.
func (d ManagementPageCommonData) quicQuery() string {
	if _, alpn, ok := strings.Cut(d.QUICAddr, "/"); ok && alpn != "" {
		return "alpn=" + url.QueryEscape(alpn)
	}
	return ""
}

// bridgeURL is the launch link of the primary bridge carrier. kcp clients
// connect over QUIC; the display address is the fallback when that carrier is
// not enabled.
func (d ManagementPageCommonData) bridgeURL(urls map[string]string) string {
	if u := urls["auto"]; u != "" {
		return u
	}
	scheme := d.BridgeType
	if scheme == "kcp" {
		scheme = "quic"
	}
	if u := urls[scheme]; u != "" {
		return u
	}
	addr, rest, _ := strings.Cut(d.BridgeAddr, "/")
	switch scheme {
	case "quic":
		if rest == "" {
			return linkURL(scheme, addr, "", "")
		}
		return linkURL(scheme, addr, "", "alpn="+url.QueryEscape(rest))
	case "ws", "wss":
		return linkURL(scheme, addr, rest, withBinary(""))
	case "tcp", "tls":
		return linkURL(scheme, addr, "", "")
	}
	return linkURL("tcp", addr, "", "")
}
