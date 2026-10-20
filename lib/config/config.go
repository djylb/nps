package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
)

type CommonConfig struct {
	Server           string
	VKey             string
	Tp               string //bridgeType kcp or tcp
	AutoReconnection bool
	TlsEnable        bool
	ProxyUrl         string
	LocalIP          string
	DnsServer        string
	NtpServer        string
	NtpInterval      int
	Client           *file.Client
	DisconnectTime   int
}

type LocalServer struct {
	Type       string
	Port       int
	Ip         string
	Password   string
	Target     string
	TargetType string
	Fallback   bool
	LocalProxy bool
}

type Config struct {
	content      string
	title        []string
	CommonConfig *CommonConfig
	Hosts        []*file.Host
	Tasks        []*file.Tunnel
	Healths      []*file.Health
	LocalServer  []*LocalServer
}

// NewConfig loads and parses an npc configuration file.
func NewConfig(path string) (*Config, error) {
	b, err := common.ReadAllFromFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfig(string(b))
}

func parseConfig(raw string) (*Config, error) {
	content, err := common.ParseStr(common.NormalizeNewlines(raw))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	sections, err := parseSections(content)
	if err != nil {
		return nil, err
	}
	c := &Config{content: content}
	for _, sec := range sections {
		c.title = append(c.title, "["+sec.name+"]")
		name := strings.ToLower(sec.name)
		keys := sectionKeys(sec.body)
		switch {
		case strings.HasPrefix(name, "secret") && !keys["mode"]:
			local := delLocalService(sec.body)
			local.Type = "secret"
			c.LocalServer = append(c.LocalServer, local)
		case strings.HasPrefix(name, "p2p") && !keys["mode"]:
			local := delLocalService(sec.body)
			if local.Type == "" {
				local.Type = "p2p"
			}
			c.LocalServer = append(c.LocalServer, local)
		case strings.HasPrefix(name, "health"):
			c.Healths = append(c.Healths, dealHealth(sec.body))
		case name == "common":
			c.CommonConfig = dealCommon(sec.body)
		case keys["host"]:
			h := dealHost(sec.body)
			h.Remark = sec.name
			c.Hosts = append(c.Hosts, h)
		default:
			t := dealTunnel(sec.body)
			t.Remark = sec.name
			c.Tasks = append(c.Tasks, t)
		}
	}
	if c.CommonConfig == nil {
		return nil, errors.New("missing [common] section")
	}
	return c, nil
}

type section struct {
	name string
	body string // trimmed, non-empty, non-comment lines joined by "\n"
}

var titleRE = regexp.MustCompile(`^\[([^\[\]]+)]$`)

// parseSections splits normalized content into sections line by line.
// Lines before the first section header are ignored.
func parseSections(content string) ([]section, error) {
	var sections []section
	var body []string
	seen := make(map[string]bool)
	flush := func() {
		if len(sections) > 0 {
			sections[len(sections)-1].body = strings.Join(body, "\n")
		}
		body = body[:0]
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := titleRE.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if seen[key] {
				return nil, fmt.Errorf("item names [%s] are not allowed to be duplicated", name)
			}
			seen[key] = true
			flush()
			sections = append(sections, section{name: name})
			continue
		}
		body = append(body, line)
	}
	flush()
	return sections, nil
}

// sectionKeys returns the set of keys present in a section body.
func sectionKeys(body string) map[string]bool {
	keys := make(map[string]bool)
	for _, line := range splitStr(body) {
		if item, ok := splitKV(line); ok {
			keys[item[0]] = true
		}
	}
	return keys
}

// splitKV splits a "key=value" line at the first '='. Both parts are
// trimmed; a line without '=' yields an empty value.
func splitKV(line string) (item [2]string, ok bool) {
	k, v, _ := strings.Cut(line, "=")
	item[0], item[1] = strings.TrimSpace(k), strings.TrimSpace(v)
	return item, item[0] != ""
}

func dealCommon(s string) *CommonConfig {
	c := new(CommonConfig)
	c.Tp = "tcp"
	c.AutoReconnection = true
	c.Client = file.NewClient("", true, true)
	c.Client.Cnf = new(file.Config)
	for _, v := range splitStr(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		switch item[0] {
		case "server_addr":
			c.Server = item[1]
		case "vkey":
			c.VKey = item[1]
		case "conn_type":
			c.Tp = item[1]
		case "auto_reconnection":
			c.AutoReconnection = common.GetBoolByStr(item[1])
		case "basic_username":
			c.Client.Cnf.U = item[1]
		case "basic_password":
			c.Client.Cnf.P = item[1]
		case "web_password":
			c.Client.WebPassword = item[1]
		case "web_username":
			c.Client.WebUserName = item[1]
		case "compress":
			c.Client.Cnf.Compress = common.GetBoolByStr(item[1])
		case "crypt":
			c.Client.Cnf.Crypt = common.GetBoolByStr(item[1])
		case "proxy_url":
			c.ProxyUrl = item[1]
		case "local_ip":
			c.LocalIP = item[1]
		case "dns_server":
			c.DnsServer = item[1]
		case "ntp_server":
			c.NtpServer = item[1]
		case "ntp_interval":
			c.NtpInterval = common.GetIntNoErrByStr(item[1])
		case "rate_limit":
			c.Client.RateLimit = common.GetIntNoErrByStr(item[1])
		case "flow_limit":
			c.Client.Flow.FlowLimit = int64(common.GetIntNoErrByStr(item[1]))
		case "time_limit":
			c.Client.Flow.TimeLimit = common.GetTimeNoErrByStr(item[1])
		case "max_conn":
			c.Client.SetMaxConn(common.GetIntNoErrByStr(item[1]))
		case "remark":
			c.Client.Remark = item[1]
		case "pprof_addr":
			common.InitPProfByAddr(item[1])
		case "disconnect_timeout":
			c.DisconnectTime = common.GetIntNoErrByStr(item[1])
		case "tls_enable":
			c.TlsEnable = common.GetBoolByStr(item[1])
		}
	}
	return c
}

func dealHost(s string) *file.Host {
	h := new(file.Host)
	h.Target = new(file.Target)
	h.Scheme = "all"
	h.MultiAccount = new(file.MultiAccount)
	var headerChange, respHeaderChange string
	for _, v := range splitStr(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		switch item[0] {
		case "host":
			h.Host = item[1]
		case "upload_limit":
			h.UploadLimit = max(common.GetIntNoErrByStr(item[1]), 0)
		case "download_limit":
			h.DownloadLimit = max(common.GetIntNoErrByStr(item[1]), 0)
		case "target_addr":
			h.Target.TargetStr = strings.ReplaceAll(item[1], ",", "\n")
		case "proxy_protocol":
			h.Target.ProxyProtocol = common.GetIntNoErrByStr(item[1])
		case "host_change":
			h.HostChange = item[1]
		case "scheme":
			h.Scheme = item[1]
		case "location":
			h.Location = item[1]
		case "path_rewrite":
			h.PathRewrite = item[1]
		case "cert_file":
			h.CertFile, _ = common.GetCertContent(item[1], "CERTIFICATE")
		case "key_file":
			h.KeyFile, _ = common.GetCertContent(item[1], "PRIVATE")
		case "https_just_proxy":
			h.HttpsJustProxy = common.GetBoolByStr(item[1])
		case "auto_ssl":
			h.AutoSSL = common.GetBoolByStr(item[1])
		case "auto_https":
			h.AutoHttps = common.GetBoolByStr(item[1])
		case "auto_cors":
			h.AutoCORS = common.GetBoolByStr(item[1])
		case "compat_mode":
			h.CompatMode = common.GetBoolByStr(item[1])
		case "redirect_url":
			h.RedirectURL = item[1]
		case "target_is_https":
			h.TargetIsHttps = common.GetBoolByStr(item[1])
		case "multi_account":
			if common.FileExists(item[1]) {
				if b, err := common.ReadAllFromFile(item[1]); err != nil {
					panic(err)
				} else {
					if content, err := common.ParseStr(string(b)); err != nil {
						panic(err)
					} else {
						h.MultiAccount.Content = content
						h.MultiAccount.AccountMap = dealMultiUser(content)
					}
				}
			}
		default:
			if name, ok := strings.CutPrefix(item[0], "header_"); ok {
				headerChange += name + ":" + item[1] + "\n"
			}
			if name, ok := strings.CutPrefix(item[0], "response_"); ok {
				respHeaderChange += name + ":" + item[1] + "\n"
			}
			h.HeaderChange = headerChange
			h.RespHeaderChange = respHeaderChange
		}
	}
	return h
}

func dealHealth(s string) *file.Health {
	h := &file.Health{}
	for _, v := range splitStr(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		switch item[0] {
		case "health_check_timeout":
			h.HealthCheckTimeout = common.GetIntNoErrByStr(item[1])
		case "health_check_max_failed":
			h.HealthMaxFail = common.GetIntNoErrByStr(item[1])
		case "health_check_interval":
			h.HealthCheckInterval = common.GetIntNoErrByStr(item[1])
		case "health_http_url":
			h.HttpHealthUrl = item[1]
		case "health_check_type":
			h.HealthCheckType = item[1]
		case "health_check_target":
			h.HealthCheckTarget = item[1]
		}
	}
	return h
}

func dealTunnel(s string) *file.Tunnel {
	t := new(file.Tunnel)
	t.Target = new(file.Target)
	t.MultiAccount = new(file.MultiAccount)
	for _, v := range splitStr(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		switch item[0] {
		case "max_conn":
			t.SetMaxConn(common.GetIntNoErrByStr(item[1]))
		case "upload_limit":
			t.UploadLimit = max(common.GetIntNoErrByStr(item[1]), 0)
		case "download_limit":
			t.DownloadLimit = max(common.GetIntNoErrByStr(item[1]), 0)
		case "server_port":
			t.Ports = item[1]
		case "server_ip":
			t.ServerIp = item[1]
		case "mode":
			t.Mode = item[1]
		case "target_addr":
			t.Target.TargetStr = strings.ReplaceAll(item[1], ",", "\n")
		case "proxy_protocol":
			t.Target.ProxyProtocol = common.GetIntNoErrByStr(item[1])
		case "target_port":
			t.Target.TargetStr = item[1]
		case "target_ip":
			t.TargetAddr = item[1]
		case "password":
			t.Password = item[1]
		case "socks5_proxy":
			t.Socks5Proxy = common.GetBoolByStr(item[1])
		case "http_proxy":
			t.HttpProxy = common.GetBoolByStr(item[1])
		case "dest_acl_mode":
			t.DestAclMode = common.GetIntNoErrByStr(item[1])
		case "dest_acl_rules":
			t.DestAclRules = strings.ReplaceAll(item[1], ",", "\n")
		case "local_path":
			t.LocalPath = item[1]
		case "strip_pre":
			t.StripPre = item[1]
		case "read_only":
			t.ReadOnly = common.GetBoolByStr(item[1])
		case "multi_account":
			if common.FileExists(item[1]) {
				if b, err := common.ReadAllFromFile(item[1]); err != nil {
					panic(err)
				} else {
					if content, err := common.ParseStr(string(b)); err != nil {
						panic(err)
					} else {
						t.MultiAccount.Content = content
						t.MultiAccount.AccountMap = dealMultiUser(content)
					}
				}
			}
		}
	}
	return t

}

func dealMultiUser(s string) map[string]string {
	multiUserMap := make(map[string]string)
	for _, line := range splitStr(s) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		var key, val string
		if idx >= 0 {
			key = strings.TrimSpace(line[:idx])
			val = strings.TrimSpace(line[idx+1:])
		} else {
			key = line
			val = ""
		}
		if key != "" {
			multiUserMap[key] = val
		}
	}
	return multiUserMap
}

func delLocalService(s string) *LocalServer {
	l := new(LocalServer)
	for _, v := range splitStr(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		switch item[0] {
		case "local_port":
			l.Port = common.GetIntNoErrByStr(item[1])
		case "local_type":
			l.Type = item[1]
		case "local_ip":
			l.Ip = item[1]
		case "password":
			l.Password = item[1]
		case "target_addr":
			l.Target = item[1]
		case "target_type":
			l.TargetType = item[1]
		case "local_proxy":
			l.LocalProxy = common.GetBoolByStr(item[1])
		case "fallback_secret":
			l.Fallback = common.GetBoolByStr(item[1])
		}
	}
	return l
}

func getAllTitle(content string) ([]string, error) {
	sections, err := parseSections(common.NormalizeNewlines(content))
	if err != nil {
		return nil, err
	}
	arr := make([]string, 0, len(sections))
	for _, sec := range sections {
		arr = append(arr, "["+sec.name+"]")
	}
	return arr, nil
}

func splitStr(s string) []string {
	return strings.Split(common.NormalizeNewlines(s), "\n")
}
