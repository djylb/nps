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

func NewConfig(path string) (c *Config, err error) {
	c = new(Config)
	var b []byte
	if b, err = common.ReadAllFromFile(path); err != nil {
		return
	} else {
		if c.content, err = common.ParseStr(string(b)); err != nil {
			return nil, err
		}
		if c.title, err = getAllTitle(c.content); err != nil {
			return
		}
		var nowIndex int
		var nextIndex int
		var nowContent string
		for i := 0; i < len(c.title); i++ {
			nowIndex = strings.Index(c.content, c.title[i]) + len(c.title[i])
			if i < len(c.title)-1 {
				nextIndex = strings.Index(c.content, c.title[i+1])
			} else {
				nextIndex = len(c.content)
			}
			nowContent = c.content[nowIndex:nextIndex]
			nowContent = stripCommentLines(nowContent)
			if strings.HasPrefix(getTitleContent(c.title[i]), "secret") && !strings.Contains(nowContent, "mode") {
				local := delLocalService(nowContent)
				local.Type = "secret"
				c.LocalServer = append(c.LocalServer, local)
				continue
			}
			if strings.HasPrefix(getTitleContent(c.title[i]), "p2p") && !strings.Contains(nowContent, "mode") {
				local := delLocalService(nowContent)
				if local.Type == "" {
					local.Type = "p2p"
				}
				c.LocalServer = append(c.LocalServer, local)
				continue
			}
			//health set
			if strings.HasPrefix(getTitleContent(c.title[i]), "health") {
				c.Healths = append(c.Healths, dealHealth(nowContent))
				continue
			}
			switch c.title[i] {
			case "[common]":
				c.CommonConfig = dealCommon(nowContent)
			default:
				if strings.Index(nowContent, "host") > -1 {
					h := dealHost(nowContent)
					h.Remark = getTitleContent(c.title[i])
					c.Hosts = append(c.Hosts, h)
				} else {
					t := dealTunnel(nowContent)
					t.Remark = getTitleContent(c.title[i])
					c.Tasks = append(c.Tasks, t)
				}
			}
		}
	}
	return
}

var bracketRE = regexp.MustCompile(`[\[\]]`)

func getTitleContent(s string) string {
	return bracketRE.ReplaceAllString(s, "")
}

var commentLineRE = regexp.MustCompile(`(?m)^[ \t]*#.*(\r?\n|$)`)

func stripCommentLines(s string) string {
	return commentLineRE.ReplaceAllString(s, "")
}

func dealCommon(s string) *CommonConfig {
	c := new(CommonConfig)
	c.Tp = "tcp"
	c.AutoReconnection = true
	c.Client = file.NewClient("", true, true)
	c.Client.Cnf = new(file.Config)
	for _, v := range splitStr(s) {
		item := strings.Split(v, "=")
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
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
			c.Client.MaxConn = common.GetIntNoErrByStr(item[1])
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
		item := strings.Split(v, "=")
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
		}
		switch strings.TrimSpace(item[0]) {
		case "host":
			h.Host = item[1]
		case "target_addr":
			h.Target.TargetStr = strings.Replace(item[1], ",", "\n", -1)
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
			if strings.Contains(item[0], "header_") {
				headerChange += strings.Replace(item[0], "header_", "", -1) + ":" + item[1] + "\n"
			}
			if strings.Contains(item[0], "response_") {
				respHeaderChange += strings.Replace(item[0], "response_", "", -1) + ":" + item[1] + "\n"
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
		item := strings.Split(v, "=")
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
		}
		switch strings.TrimSpace(item[0]) {
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
		item := strings.Split(v, "=")
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
		}
		switch strings.TrimSpace(item[0]) {
		case "server_port":
			t.Ports = item[1]
		case "server_ip":
			t.ServerIp = item[1]
		case "mode":
			t.Mode = item[1]
		case "target_addr":
			t.Target.TargetStr = strings.Replace(item[1], ",", "\n", -1)
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
			t.DestAclRules = strings.Replace(item[1], ",", "\n", -1)
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
		item := strings.Split(v, "=")
		if len(item) == 0 {
			continue
		} else if len(item) == 1 {
			item = append(item, "")
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

func getAllTitle(content string) (arr []string, err error) {
	var re *regexp.Regexp
	re, err = regexp.Compile(`(?m)^\[[^\[\]\r\n]+\]`)
	if err != nil {
		return
	}
	arr = re.FindAllString(content, -1)
	m := make(map[string]bool)
	for _, v := range arr {
		if _, ok := m[v]; ok {
			err = errors.New(fmt.Sprintf("Item names %s are not allowed to be duplicated", v))
			return
		}
		m[v] = true
	}
	return
}

func splitStr(s string) (configDataArr []string) {
	if common.IsWindows() {
		configDataArr = strings.Split(s, "\r\n")
	}
	if len(configDataArr) < 3 {
		configDataArr = strings.Split(s, "\n")
	}
	return
}
