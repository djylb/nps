package ui

import (
	"html/template"
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
	if d.TCPIP != "" {
		data["tcp_ip"] = urlHost(d.TCPIP)
	}
	if d.TCPPort != "" {
		data["tcp_p"] = d.TCPPort
	}
	if d.KCPPort != "" {
		data["kcp_p"] = d.KCPPort
	}
	if d.TLSIP != "" {
		data["tls_ip"] = urlHost(d.TLSIP)
	}
	if d.TLSPort != "" {
		data["tls_p"] = d.TLSPort
	}
	if d.QUICIP != "" {
		data["quic_ip"] = urlHost(d.QUICIP)
	}
	if d.QUICPort != "" {
		data["quic_p"] = d.QUICPort
	}
	if d.QUICALPN != "" {
		data["quic_alpn"] = d.QUICALPN
	}
	// the ALPN of the display address "host:port/alpn", as a URL query
	if _, alpn, ok := strings.Cut(d.QUICAddr, "/"); ok && alpn != "" {
		data["quic_query"] = "?alpn=" + url.QueryEscape(alpn)
	}
	if d.WSPath != "" {
		data["ws_path"] = d.WSPath
	}
	if d.WSIP != "" {
		data["ws_ip"] = urlHost(d.WSIP)
	}
	if d.WSPort != "" {
		data["ws_p"] = d.WSPort
	}
	if d.WSSIP != "" {
		data["wss_ip"] = urlHost(d.WSSIP)
	}
	if d.WSSPort != "" {
		data["wss_p"] = d.WSSPort
	}
	return data
}

// urlHost writes host as the host of a URL: an IPv6 address in brackets.
func urlHost(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}
