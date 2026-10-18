package ui

import "html/template"

type LoginPageCommonData struct {
	WebBaseURL     string
	HeadCustomCode template.HTML
	Version        string
	Year           int
	CaptchaOpen    bool
	CaptchaHTML    template.HTML
}

func (d LoginPageCommonData) Map() map[string]interface{} {
	data := map[string]interface{}{
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
	ServerUpdate  bool
	Username      string
	BridgeType    string
	BridgeAddr    string
	BridgeIP      string
	BridgePort    string
	WindowsSuffix string
	TCPIP         string
	TCPPort       string
	KCPIP         string
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
	// ClientUpdateMin is the oldest npc version that acts on update requests.
	ClientUpdateMin string
	// Features are the feature switches the templates test, by name.
	Features map[string]bool
}

func (d ManagementPageCommonData) Map() map[string]interface{} {
	data := map[string]interface{}{
		"web_base_url":        d.WebBaseURL,
		"head_custom_code":    d.HeadCustomCode,
		"version":             d.Version,
		"year":                d.Year,
		"isAdmin":             d.IsAdmin,
		"allow_server_update": d.IsAdmin && d.ServerUpdate,
		"username":            d.Username,
		"bridgeType":          d.BridgeType,
		"addr":                d.BridgeAddr,
		"ip":                  d.BridgeIP,
		"p":                   d.BridgePort,
		"proxyPort":           d.ProxyPort,
		"client_update_min":   d.ClientUpdateMin,
	}
	for name, on := range d.Features {
		data[name] = on
	}
	if d.WindowsSuffix != "" {
		data["win"] = d.WindowsSuffix
	}
	if d.TCPIP != "" {
		data["tcp_ip"] = d.TCPIP
	}
	if d.TCPPort != "" {
		data["tcp_p"] = d.TCPPort
	}
	if d.KCPIP != "" {
		data["kcp_ip"] = d.KCPIP
	}
	if d.KCPPort != "" {
		data["kcp_p"] = d.KCPPort
	}
	if d.TLSIP != "" {
		data["tls_ip"] = d.TLSIP
	}
	if d.TLSPort != "" {
		data["tls_p"] = d.TLSPort
	}
	if d.QUICIP != "" {
		data["quic_ip"] = d.QUICIP
	}
	if d.QUICPort != "" {
		data["quic_p"] = d.QUICPort
	}
	if d.QUICALPN != "" {
		data["quic_alpn"] = d.QUICALPN
	}
	if d.QUICAddr != "" {
		data["quic_addr"] = d.QUICAddr
	}
	if d.WSPath != "" {
		data["ws_path"] = d.WSPath
	}
	if d.WSIP != "" {
		data["ws_ip"] = d.WSIP
	}
	if d.WSPort != "" {
		data["ws_p"] = d.WSPort
	}
	if d.WSSIP != "" {
		data["wss_ip"] = d.WSSIP
	}
	if d.WSSPort != "" {
		data["wss_p"] = d.WSSPort
	}
	return data
}
