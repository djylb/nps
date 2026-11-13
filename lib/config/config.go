package config

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"regexp"
	"strings"

	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/slogx"
)

var log = slogx.Module("config")

type CommonConfig struct {
	Server           string
	VKey             string
	Tp               string //bridgeType kcp or tcp
	Pipes            string // connprofile.PipesSingle or PipesMulti
	SchedulerUp      string // connprofile.Schedulers, new protocol
	AutoReconnection bool
	ProxyUrl         string
	LocalIP          string
	DnsServer        string
	NtpServer        string
	NtpInterval      int
	Client           *model.Client
	DisconnectTime   int
	// Resident: a connection with local accesses also runs as an ordinary
	// resident client (launch key resident).
	Resident bool
	CoreSettings
}

// CoreSettings are the [common] keys of the new protocol; the zero value of
// each is the protocol's own default.
type CoreSettings struct {
	MaxLanes      int    // session_max_lanes: 0 is 16, 1 turns lanes off
	RelayIdle     int    // relay_idle, seconds: 0 is 60
	KeepServer    bool   // p2p_visitor_keep_server
	ResumeTimeout int    // session_resume_timeout, seconds: 0 is 30, negative no resume
	UsageInterval int    // session_usage_interval, seconds between reports of direct traffic: 0 is 60, negative never
	Pin           string // new_protocol_pin: hex key id the server must have, "" for none
}

// Defaults are the values a [common] section starts from. The command line
// sets them before any file is read, so that a flag is the default of every
// file and launch payload that does not set the key itself.
type Defaults struct {
	Pipes       string
	SchedulerUp string
	CoreSettings
}

// CommonDefaults is set once at start-up, before configuration is parsed.
var CommonDefaults Defaults

// NewCommonConfig returns a [common] section with no key set: the defaults of
// CommonDefaults and of the keys. dealCommon and the command line build their
// connection settings from it, so that both have the same defaults.
func NewCommonConfig() *CommonConfig {
	c := &CommonConfig{
		Tp:               "tcp",
		AutoReconnection: true,
		Pipes:            CommonDefaults.Pipes,
		SchedulerUp:      CommonDefaults.SchedulerUp,
		CoreSettings:     CommonDefaults.CoreSettings,
		Client:           model.NewClient("", true, true),
	}
	c.Client.Cnf = new(model.Config)
	return c
}

type Config struct {
	content      string
	title        []string
	CommonConfig *CommonConfig
	Hosts        []*model.Host
	Tasks        []*model.Tunnel
	LocalServer  []*LocalServer
	legacy       bool // settings of an earlier version were converted (legacy.go)
}

// NewConfig loads and parses an npc configuration file.
func NewConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfig(string(b))
}

// Parse parses npc configuration text, as NewConfig does for a file.
func Parse(content string) (*Config, error) {
	return parseConfig(content)
}

// ParseUntrusted parses configuration text from a source other than the local
// user. Nothing in it reaches the local machine: templates and the keys in
// localKeys are refused before any section is read, so parsing reads no file
// and opens no listener.
func ParseUntrusted(raw string) (*Config, error) {
	content := textutil.NormalizeNewlines(raw)
	if strings.Contains(content, "{{") { // templates can read the environment
		return nil, errors.New("untrusted configuration may not use templates")
	}
	sections, err := parseSections(content)
	if err != nil {
		return nil, err
	}
	for _, sec := range sections {
		for _, line := range textutil.SplitLines(sec.body) {
			if item, ok := splitKV(line); ok && localKeys[item[0]] {
				return nil, fmt.Errorf("untrusted configuration may not set %q", item[0])
			}
		}
	}
	c, err := buildConfig(content, sections)
	if err != nil {
		return nil, err
	}
	return c, c.checkUntrusted()
}

// localKeys reach the local machine's files, open debug listeners or change
// the resolver of the whole process, which only the local user may ask for.
var localKeys = map[string]bool{
	"multi_account": true, "cert_file": true, "key_file": true,
	"local_path": true, "strip_pre": true, "pprof_addr": true, "dns_server": true,
}

// checkUntrusted refuses a parsed configuration that serves or reads local
// files, whichever key led to it.
func (c *Config) checkUntrusted() error {
	for _, t := range c.Tasks {
		if strings.EqualFold(t.Mode, "file") || t.LocalPath != "" || t.StripPre != "" || t.MultiAccount.Content != "" {
			return fmt.Errorf("untrusted configuration may not set local files in %q", t.Remark)
		}
	}
	for _, h := range c.Hosts {
		if h.CertFile != "" || h.KeyFile != "" || h.MultiAccount.Content != "" {
			return fmt.Errorf("untrusted configuration may not set local files in %q", h.Remark)
		}
	}
	return nil
}

func parseConfig(raw string) (*Config, error) {
	content, err := parseEnvTemplate(textutil.NormalizeNewlines(raw))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	sections, err := parseSections(content)
	if err != nil {
		return nil, err
	}
	return buildConfig(content, sections)
}

func buildConfig(content string, sections []section) (*Config, error) {
	c := &Config{content: content}
	for _, sec := range sections {
		c.title = append(c.title, "["+sec.name+"]")
		name := strings.ToLower(sec.name)
		keys := sectionKeys(sec.body)
		switch {
		case strings.HasPrefix(name, "secret") && !keys["mode"]:
			l, old := delLocalService(sec.name, false, sec.body)
			c.LocalServer, c.legacy = append(c.LocalServer, l), c.legacy || old
		case legacyLocalSection(name, keys):
			l, _ := delLocalService(sec.name, true, sec.body)
			c.LocalServer, c.legacy = append(c.LocalServer, l), true
		case legacySection(keys) != "":
			log.Warn("legacy config section ignored", "name", sec.name, "detail", legacySection(keys))
		case name == "common":
			c.CommonConfig = dealCommon(sec.body)
		case keys["host"]:
			h, err := dealHost(sec.body)
			if err != nil {
				return nil, fmt.Errorf("[%s]: %w", sec.name, err)
			}
			h.Remark = sec.name
			c.Hosts = append(c.Hosts, h)
		default:
			t, err := dealTunnel(sec.body)
			if err != nil {
				return nil, fmt.Errorf("[%s]: %w", sec.name, err)
			}
			t.Remark = sec.name
			c.Tasks = append(c.Tasks, t)
			c.legacy = c.legacy || legacyTunnelMode(t)
		}
	}
	if c.CommonConfig == nil {
		return nil, errors.New("missing [common] section")
	}
	if err := c.CommonConfig.checkConnection(); err != nil {
		return nil, fmt.Errorf("[common]: %w", err)
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
	for line := range strings.SplitSeq(content, "\n") {
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
	for _, line := range textutil.SplitLines(body) {
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
	c := NewCommonConfig()
	var legacy legacyCommon
	for _, v := range textutil.SplitLines(s) {
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
		case "pipes":
			c.Pipes = item[1]
		case "scheduler_up":
			c.SchedulerUp = item[1]
		case "session_max_lanes":
			c.MaxLanes = textutil.GetIntNoErrByStr(item[1])
		case "relay_idle":
			c.RelayIdle = textutil.GetIntNoErrByStr(item[1])
		case "p2p_visitor_keep_server":
			c.KeepServer = parseBool(item)
		case "resident":
			c.Resident = parseBool(item)
		case "session_resume_timeout":
			c.ResumeTimeout = textutil.GetIntNoErrByStr(item[1])
		case "session_usage_interval":
			c.UsageInterval = textutil.GetIntNoErrByStr(item[1])
		case "new_protocol_pin":
			c.Pin = item[1]
		case "auto_reconnection":
			c.AutoReconnection = parseBool(item)
		case "basic_username":
			c.Client.Cnf.U = item[1]
		case "basic_password":
			c.Client.Cnf.P = item[1]
		case "web_password":
			c.Client.WebPassword = item[1]
		case "web_username":
			c.Client.WebUserName = item[1]
		case "compress":
			c.Client.Cnf.Compress = parseBool(item)
		case "crypt":
			c.Client.Cnf.Crypt = parseBool(item)
		case "proxy_url":
			c.ProxyUrl = item[1]
		case "local_ip":
			c.LocalIP = item[1]
		case "dns_server":
			c.DnsServer = item[1]
		case "ntp_server":
			c.NtpServer = item[1]
		case "ntp_interval":
			c.NtpInterval = textutil.GetIntNoErrByStr(item[1])
		case "rate_limit":
			c.Client.RateLimit = textutil.GetIntNoErrByStr(item[1])
		case "flow_limit":
			c.Client.Flow.FlowLimit = int64(textutil.GetIntNoErrByStr(item[1]))
		case "time_limit":
			c.Client.Flow.TimeLimit = textutil.GetTimeNoErrByStr(item[1])
		case "max_conn":
			c.Client.SetMaxConn(textutil.GetIntNoErrByStr(item[1]))
		case "remark":
			c.Client.Remark = item[1]
		case "pprof_addr":
			runenv.InitPProfByAddr(item[1])
		case "disconnect_timeout":
			c.DisconnectTime = textutil.GetIntNoErrByStr(item[1])
		default:
			legacy.read(item)
		}
	}
	legacy.apply(c)
	return c
}

// checkConnection checks server_addr, a profile of carrier candidates, and
// pipes, and sets Pipes to its canonical value.
func (c *CommonConfig) checkConnection() error {
	pipes, err := connprofile.ParsePipes(c.Pipes)
	if err != nil {
		return err
	}
	c.Pipes = pipes
	if c.SchedulerUp, err = connprofile.ParseScheduler(c.SchedulerUp); err != nil {
		return err
	}
	if strings.TrimSpace(c.Server) == "" {
		return nil
	}
	_, err = connprofile.Parse(c.Server, c.Tp)
	return err
}

func dealHost(s string) (*model.Host, error) {
	h := &model.Host{Target: new(model.Target), MultiAccount: new(model.MultiAccount)}
	setConfDefaults(model.HostSettings, h)
	var headerChange, respHeaderChange string
	for _, v := range textutil.SplitLines(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		if item[0] == "multi_account" {
			if err := loadMultiAccount(h.MultiAccount, item[1]); err != nil {
				return nil, err
			}
			continue
		}
		if setConf(hostConf, h, item) {
			continue
		}
		if name, ok := strings.CutPrefix(item[0], "header_"); ok {
			headerChange += name + ":" + item[1] + "\n"
		}
		if name, ok := strings.CutPrefix(item[0], "response_"); ok {
			respHeaderChange += name + ":" + item[1] + "\n"
		}
	}
	h.HeaderChange, h.RespHeaderChange = headerChange, respHeaderChange
	return h, nil
}

// The npc.conf keys of model.TunnelSettings and model.HostSettings.
var tunnelConf, hostConf = confSettings(model.TunnelSettings), confSettings(model.HostSettings)

func confSettings(settings []model.EntrySetting) map[string]model.EntrySetting {
	m := make(map[string]model.EntrySetting)
	for _, s := range settings {
		if s.Conf != "" {
			m[s.Conf] = s
		}
	}
	return m
}

func setConfDefaults(settings []model.EntrySetting, entry any) {
	for _, s := range settings {
		if s.Conf != "" && s.ConfDefault != "" {
			_ = s.Set(entry, s.ConfDefault)
		}
	}
}

// setConf sets the setting item names on entry, reporting whether there is
// one.
func setConf(settings map[string]model.EntrySetting, entry any, item [2]string) bool {
	s, ok := settings[item[0]]
	if !ok {
		return false
	}
	v := item[1]
	if s.ConfList {
		v = strings.ReplaceAll(v, ",", "\n")
	}
	if s.ConfPEM != "" {
		v, _ = crypt.GetCertContent(v, s.ConfPEM)
	}
	if s.Set(entry, v) != nil {
		log.Warn("config value is not a boolean, using false", "keys", item[0], "value", item[1])
	}
	return true
}

// loadMultiAccount fills m from the account file at path, if there is one.
func loadMultiAccount(m *model.MultiAccount, path string) error {
	if !runenv.FileExists(path) {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("multi_account: %w", err)
	}
	content, err := parseEnvTemplate(string(b))
	if err != nil {
		return fmt.Errorf("multi_account: %w", err)
	}
	m.Content = content
	m.AccountMap = dealMultiUser(content)
	return nil
}

func dealTunnel(s string) (*model.Tunnel, error) {
	t := &model.Tunnel{Target: new(model.Target), MultiAccount: new(model.MultiAccount)}
	setConfDefaults(model.TunnelSettings, t)
	for _, v := range textutil.SplitLines(s) {
		item, ok := splitKV(v)
		if !ok {
			continue
		}
		if item[0] == "multi_account" {
			if err := loadMultiAccount(t.MultiAccount, item[1]); err != nil {
				return nil, err
			}
			continue
		}
		setConf(tunnelConf, t, item)
	}
	return t, nil
}

func dealMultiUser(s string) map[string]string {
	multiUserMap := make(map[string]string)
	for _, line := range textutil.SplitLines(s) {
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

// delLocalService reads a point-to-point access section. legacy is true for
// a section of an earlier version ([p2p...]); old reports whether keys of
// an earlier version were converted.
func delLocalService(name string, legacy bool, s string) (l *LocalServer, old bool) {
	l = new(LocalServer)
	prev := legacyLocal{section: legacy}
	for _, v := range textutil.SplitLines(s) {
		item, ok := splitKV(v)
		if !ok || prev.read(item) {
			continue
		}
		switch item[0] {
		case "local_port":
			l.Port = textutil.GetIntNoErrByStr(item[1])
		case "local_type":
			l.Type = strings.ToLower(strings.TrimSpace(item[1]))
		case "conn_mode":
			l.ConnMode = strings.ToLower(strings.TrimSpace(item[1]))
		case "local_ip":
			l.Ip = item[1]
		case "password":
			l.Password = item[1]
		case "target_addr":
			l.Target = item[1]
		case "local_proxy":
			l.LocalProxy = parseBool(item)
		}
	}
	old = prev.apply(name, l)
	if l.Type == "" {
		l.Type = LocalTCPUDP
	}
	if l.ConnMode == "" {
		l.ConnMode = ConnAuto
	}
	return l, old
}

func getAllTitle(content string) ([]string, error) {
	sections, err := parseSections(textutil.NormalizeNewlines(content))
	if err != nil {
		return nil, err
	}
	arr := make([]string, 0, len(sections))
	for _, sec := range sections {
		arr = append(arr, "["+sec.name+"]")
	}
	return arr, nil
}

// parseEnvTemplate executes str as a text template over the environment.
func parseEnvTemplate(str string) (string, error) {
	tmp := template.New("npc")
	w := new(bytes.Buffer)
	tmp, err := tmp.Parse(str)
	if err != nil {
		return "", err
	}
	if err = tmp.Execute(w, runenv.GetEnvMap()); err != nil {
		return "", err
	}
	return w.String(), nil
}

// parseBool reads the boolean value of item, warning about one that is not a
// boolean and so counts as false.
func parseBool(item [2]string) bool {
	v, err := textutil.ParseBool(item[1])
	if err != nil {
		log.Warn("config value is not a boolean, using false", "keys", item[0], "value", item[1])
	}
	return v
}
