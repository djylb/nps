package launch

// Everything that accepts the forms of earlier versions is in this file: the
// JSON, YAML and INI content of -launch files and the command line flags.
// Each ends as a Profile; nothing else knows these forms.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/textutil"
	"go.yaml.in/yaml/v3"
)

func parseLegacyINI(text string, trusted bool) ([]Profile, error) {
	return parseText("", text, trusted, false, nil)
}

func parseLegacyYAML(text string, trusted bool) ([]Profile, error) {
	var root any
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return nil, fmt.Errorf("invalid launch YAML: %w", err)
	}
	return parseStructured(root, trusted)
}

func parseLegacyJSON(text []byte, trusted bool) ([]Profile, error) {
	dec := json.NewDecoder(strings.NewReader(string(text)))
	dec.UseNumber() // keeps large integers out of exponent form
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("invalid launch JSON: %w", err)
	}
	return parseStructured(root, trusted)
}

func parseStructured(root any, trusted bool) ([]Profile, error) {
	items, isList := root.([]any)
	if !isList {
		m, ok := root.(map[string]any)
		if !ok {
			return nil, errors.New("unsupported launch payload")
		}
		items = []any{m}
		if list, ok := m["profiles"]; ok {
			if len(m) != 1 {
				return nil, errors.New(`"profiles" cannot be combined with other keys`)
			}
			if items, ok = list.([]any); !ok {
				return nil, errors.New(`"profiles" must be a list`)
			}
		}
	}
	if len(items) == 0 {
		return nil, errors.New("launch payload has no profiles")
	}
	var out []Profile
	seen := make(map[string]bool)
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("profile %d is not a mapping", i+1)
		}
		profiles, err := parseProfile(m, "", trusted)
		if err != nil {
			return nil, fmt.Errorf("profile %d: %w", i+1, err)
		}
		for _, p := range profiles {
			if p.Name != "" && seen[p.Name] {
				return nil, fmt.Errorf("duplicate profile name %q", p.Name)
			}
			seen[p.Name] = true
		}
		out = append(out, profiles...)
	}
	return out, nil
}

var shorthand = map[string]string{
	"server": "server_addr", "vkey": "vkey", "type": "conn_type",
	"proxy": "proxy_url", "local_ip": "local_ip", "pipes": "pipes",
}

type sectionKind struct {
	key  string
	name func(m map[string]any, i int) string
}

// sections are rendered in this order, each with its section name rule.
var sectionKinds = []sectionKind{
	{"hosts", namedOr("host")},
	{"tasks", namedOr("task")},
	{"locals", func(m map[string]any, i int) string {
		// a payload spells local servers the current way: give the defaults
		// of the current spelling, so the section is not read as an old one
		for key, def := range map[string]string{"local_type": config.LocalTCPUDP, "conn_mode": config.ConnAuto} {
			if _, ok := m[key]; !ok {
				m[key] = def
			}
		}
		return fmt.Sprintf("secret_%d", i)
	}},
}

func namedOr(prefix string) func(map[string]any, int) string {
	return func(m map[string]any, i int) string {
		if name, ok := m["remark"].(string); ok && name != "" {
			delete(m, "remark")
			return name
		}
		return fmt.Sprintf("%s_%d", prefix, i)
	}
}

func parseProfile(m map[string]any, fallback string, trusted bool) ([]Profile, error) {
	name := fallback
	if v, ok := m["name"]; ok {
		if name, ok = v.(string); !ok || name == "" {
			return nil, errors.New(`"name" must be a non-empty string`)
		}
	}
	base := make(map[string]any)
	direct := true
	for key, value := range m {
		if iniKey, ok := shorthand[key]; ok {
			base[iniKey] = value
			continue
		}
		if key == "common" {
			direct = false
			continue
		}
		if key == "name" {
			continue
		}
		if !slices.ContainsFunc(sectionKinds, func(k sectionKind) bool { return k.key == key }) {
			return nil, fmt.Errorf("unknown key %q", key)
		}
		direct = false
	}
	if v, ok := m["common"]; ok {
		cm, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New(`"common" must be a mapping`)
		}
		maps.Copy(base, cm)
	}
	if direct {
		return expandDirect(name, base, trusted)
	}

	var b strings.Builder
	if err := writeSection(&b, "common", base); err != nil {
		return nil, err
	}
	want := make(map[string]int)
	for _, kind := range sectionKinds {
		v, ok := m[kind.key]
		if !ok {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%q must be a list", kind.key)
		}
		for i, item := range list {
			section, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d] is not a mapping", kind.key, i)
			}
			section = maps.Clone(section)
			if kind.key == "locals" {
				if err := checkLocal(section); err != nil {
					return nil, fmt.Errorf("locals[%d]: %w", i, err)
				}
			}
			if err := writeSection(&b, kind.name(section, i+1), section); err != nil {
				return nil, err
			}
		}
		want[kind.key] = len(list)
	}
	return parseText(name, b.String(), trusted, false, want)
}

// expandDirect runs one profile per server, with the list rules of the
// command line flags: shorter lists repeat their last value.
func expandDirect(name string, values map[string]any, trusted bool) ([]Profile, error) {
	lists := make(map[string]*[]string)
	var ptrs []*[]string
	server, err := scalar(values["server_addr"])
	if err != nil {
		return nil, fmt.Errorf("server_addr: %w", err)
	}
	sep := connprofile.ListSep(server)
	for _, key := range []string{"server_addr", "vkey", "conn_type", "local_ip"} {
		text, err := scalar(values[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		list := connprofile.SplitList(text, sep)
		lists[key] = &list
		ptrs = append(ptrs, &list)
	}
	count := textutil.ExtendArrs(ptrs...)
	var out []Profile
	for i := range count {
		node := maps.Clone(values)
		for key, list := range lists {
			node[key] = (*list)[i]
		}
		if node["conn_type"] == "" {
			delete(node, "conn_type")
		}
		var b strings.Builder
		if err := writeSection(&b, "common", node); err != nil {
			return nil, err
		}
		nodeName := name
		if count > 1 && name != "" {
			nodeName = name + "#" + strconv.Itoa(i+1)
		}
		profiles, err := parseText(nodeName, b.String(), trusted, true, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, profiles...)
	}
	return out, nil
}

// checkLocal refuses a local server with keys or values npc.conf accepts only
// from earlier versions: launch payloads take the current ones alone.
func checkLocal(m map[string]any) error {
	for key, value := range m {
		var allowed []string
		switch key {
		case "local_type":
			allowed = config.LocalTypes
		case "conn_mode":
			allowed = config.ConnModes
		case "local_port", "local_ip", "password", "target_addr", "local_proxy":
			continue
		default:
			return fmt.Errorf("unknown key %q", key)
		}
		v, _ := value.(string)
		if !slices.Contains(allowed, strings.ToLower(strings.TrimSpace(v))) {
			return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// CommandLine is the connection and access flags of earlier versions.
type CommandLine struct {
	Server, VKey, Type, Proxy, LocalIP string
	Config                             []string // npc.conf paths

	Password, LocalType, ConnMode, Target string
	LocalPort                             int
	LocalProxy                            bool
}

// Sources converts the flags into launch sources. An access by -password
// replaces the other forms, as it always has.
func (f CommandLine) Sources() ([]Source, error) {
	if f.Password != "" {
		return []Source{{Label: "-password", Load: f.accessProfile}}, nil
	}
	var out []Source
	if f.Server != "" && f.VKey != "" {
		if err := f.checkServer(); err != nil {
			return nil, err
		}
		out = append(out, Source{Label: "-server", Load: f.serverProfiles})
	}
	for _, path := range f.Config {
		out = append(out, Source{Label: "-config " + path, Load: func(ctx context.Context) ([]Profile, error) {
			return configFile(ctx, path)
		}})
	}
	return out, nil
}

func (f CommandLine) checkServer() error {
	sep := connprofile.ListSep(f.Server)
	if err := connprofile.CheckList(f.Server, connprofile.SplitList(f.Type, sep)); err != nil {
		return err
	}
	return nil
}

func (f CommandLine) accessProfile(context.Context) ([]Profile, error) {
	c := config.NewCommonConfig()
	c.Server, c.VKey, c.Tp = f.Server, f.VKey, strings.ToLower(f.Type)
	c.LocalIP, c.ProxyUrl = strings.TrimSpace(f.LocalIP), f.Proxy
	if _, err := connprofile.Parse(c.Server, c.Tp); err != nil {
		return nil, err
	}
	l := &config.LocalServer{
		Type: strings.ToLower(strings.TrimSpace(f.LocalType)), ConnMode: strings.ToLower(strings.TrimSpace(f.ConnMode)),
		Port: f.LocalPort, Password: f.Password, Target: f.Target, LocalProxy: f.LocalProxy,
	}
	return []Profile{{Config: &config.Config{CommonConfig: c, LocalServer: []*config.LocalServer{l}}}}, nil
}

// serverProfiles runs one direct profile per server: shorter lists repeat
// their last value.
func (f CommandLine) serverProfiles(context.Context) ([]Profile, error) {
	server := strings.ReplaceAll(f.Server, "：", ":")
	sep := connprofile.ListSep(server)
	servers, keys := connprofile.SplitList(server, sep), connprofile.SplitList(f.VKey, sep)
	types, ips := connprofile.SplitList(f.Type, sep), connprofile.SplitList(f.LocalIP, sep)
	if len(types) == 0 {
		types = []string{"tcp"}
	}
	if len(servers) == 0 || len(keys) == 0 {
		return nil, errors.New("server and vkey must not be empty")
	}
	n := textutil.ExtendArrs(&servers, &keys, &types, &ips)
	out := make([]Profile, n)
	for i := range n {
		c := config.NewCommonConfig()
		c.Server, c.VKey, c.Tp, c.LocalIP, c.ProxyUrl = servers[i], keys[i], strings.ToLower(types[i]), ips[i], f.Proxy
		out[i] = Profile{Direct: true, Config: &config.Config{CommonConfig: c}}
	}
	return out, nil
}

// configFile reads a configuration file: JSON or JSONC of the profiles
// schema, or an npc.conf.
func configFile(ctx context.Context, path string) ([]Profile, error) {
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".jsonc" || ext == ".json" {
		return Resolve(ctx, "@"+path)
	}
	cnf, err := config.NewConfig(path)
	if err == nil && cnf.CommonConfig == nil {
		err = errors.New("no common section")
	}
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	return []Profile{{Config: cnf}}, nil
}
