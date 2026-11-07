// Package launch resolves npc quick start payloads into runnable profiles.
//
// Every form ends as npc configuration text parsed by lib/config, so that
// package stays the only definition of the configuration keys.
package launch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/textutil"
	"go.yaml.in/yaml/v3"
)

const (
	maxPayloadSize = 1 << 20
	maxDepth       = 4
)

// Profile is one runnable client. A direct profile connects with an existing
// client vkey; otherwise Config is sent to the server or runs local servers.
type Profile struct {
	Name   string
	Direct bool
	Config *config.Config
}

// Resolve turns a payload given by the local user into profiles. Payloads
// fetched from a URL are untrusted and may not refer to anything local.
func Resolve(ctx context.Context, input string) ([]Profile, error) {
	return resolve(ctx, input, true, 0)
}

func resolve(ctx context.Context, raw string, trusted bool, depth int) ([]Profile, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\xef\xbb\xbf"))
	if raw == "" {
		return nil, errors.New("launch payload is empty")
	}
	if depth > maxDepth {
		return nil, errors.New("launch payload is nested too deeply")
	}
	lower := strings.ToLower(raw)
	isURL := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
	switch {
	case (isURL || raw[0] == '@') && !trusted:
		return nil, errors.New("a fetched payload may not refer to a URL or file")
	case isURL:
		body, err := fetch(ctx, raw)
		if err != nil {
			return nil, err
		}
		profiles, err := resolve(ctx, body, false, depth+1)
		if err != nil {
			return nil, &RemoteError{Err: err}
		}
		return profiles, nil
	case raw[0] == '@':
		body, err := readFile(strings.TrimSpace(raw[1:]))
		if err != nil {
			return nil, err
		}
		return resolve(ctx, body, true, depth+1)
	case strings.HasPrefix(lower, "npc://"):
		return parseNPC(raw, trusted)
	case looksLikeINI(raw):
		return parseText("", raw, trusted, false, nil)
	case raw[0] == '{' || raw[0] == '[':
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber() // keeps large integers out of exponent form
		var root any
		if err := dec.Decode(&root); err != nil {
			return nil, fmt.Errorf("invalid launch JSON: %w", err)
		}
		return parseStructured(root, trusted)
	}
	if decoded, ok := decodeBase64(raw); ok {
		return resolve(ctx, decoded, trusted, depth+1)
	}
	var root any
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("invalid launch YAML: %w", err)
	}
	return parseStructured(root, trusted)
}

func readFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return readLimited(f)
}

var iniTitle = regexp.MustCompile(`^\[[^\[\]"{}]+]$`)

func looksLikeINI(raw string) bool {
	for line := range strings.Lines(raw) {
		line = strings.TrimSpace(line)
		if line != "" && line[0] != '#' {
			return iniTitle.MatchString(line)
		}
	}
	return false
}

func decodeBase64(raw string) (string, bool) {
	s := strings.Join(strings.Fields(raw), "")
	s = strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(s, "="))
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
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
		profiles, err := parseProfile(m, fmt.Sprintf("profile-%d", i+1), trusted)
		if err != nil {
			return nil, fmt.Errorf("profile %d: %w", i+1, err)
		}
		for _, p := range profiles {
			if seen[p.Name] {
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
	"proxy": "proxy_url", "local_ip": "local_ip",
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
		if m["local_type"] == "secret" {
			return fmt.Sprintf("secret_%d", i)
		}
		return fmt.Sprintf("p2p_%d", i)
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
	for _, key := range []string{"server_addr", "vkey", "conn_type", "local_ip"} {
		text, err := scalar(values[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		list := textutil.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(text, "，", ","), ","))
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
		if count > 1 {
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

var (
	sectionName = regexp.MustCompile(`^[^\[\]\r\n]+$`)
	safeKey     = regexp.MustCompile(`^[^=\[\]#\s]+$`)
)

func writeSection(b *strings.Builder, name string, values map[string]any) error {
	if !sectionName.MatchString(name) {
		return fmt.Errorf("invalid section name %q", name)
	}
	b.WriteString("[" + name + "]\n")
	for _, key := range slices.Sorted(maps.Keys(values)) {
		value := values[key]
		if prefix, ok := map[string]string{"headers": "header_", "response_headers": "response_"}[key]; ok {
			headers, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%q must be a mapping", key)
			}
			for _, h := range slices.Sorted(maps.Keys(headers)) {
				if err := writeKey(b, prefix+h, headers[h]); err != nil {
					return err
				}
			}
			continue
		}
		if err := writeKey(b, key, value); err != nil {
			return err
		}
	}
	return nil
}

func writeKey(b *strings.Builder, key string, value any) error {
	if !safeKey.MatchString(key) {
		return fmt.Errorf("invalid key %q", key)
	}
	text, err := scalar(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	b.WriteString(key + "=" + text + "\n")
	return nil
}

// scalar renders a value for one configuration line. Refusing line breaks
// keeps a value from adding sections of its own.
func scalar(value any) (string, error) {
	var text string
	switch v := value.(type) {
	case nil:
	case string:
		text = v
	case json.Number, bool, int, int64, uint64, float64:
		text = fmt.Sprint(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if _, nested := item.([]any); nested {
				return "", errors.New("nested list")
			}
			var err error
			if parts[i], err = scalar(item); err != nil {
				return "", err
			}
		}
		text = strings.Join(parts, ",")
	default:
		return "", fmt.Errorf("unsupported value of type %T", value)
	}
	if strings.ContainsAny(text, "\r\n") {
		return "", errors.New("value contains a line break")
	}
	return strings.TrimSpace(text), nil
}

func parseText(name, text string, trusted, direct bool, want map[string]int) ([]Profile, error) {
	parse := config.Parse
	if !trusted {
		parse = config.ParseUntrusted
	}
	cfg, err := parse(text)
	if err != nil {
		return nil, err
	}
	c := cfg.CommonConfig
	if c.Server == "" || c.VKey == "" {
		return nil, errors.New("launch payload requires a server and a vkey")
	}
	c.Tp = strings.ToLower(c.Tp)
	if want != nil && (len(cfg.Hosts) != want["hosts"] || len(cfg.Tasks) != want["tasks"] ||
		len(cfg.LocalServer) != want["locals"]) {
		return nil, errors.New(`a section name changes its kind: "secret" and "p2p" prefixes are reserved`)
	}
	for _, l := range cfg.LocalServer {
		if l.Password == "" {
			return nil, errors.New("a local server requires a password")
		}
		if l.TargetType == "" {
			l.TargetType = model.TargetAll
		}
	}
	if name == "" {
		name = "profile-1"
	}
	return []Profile{{Name: name, Direct: direct, Config: cfg}}, nil
}

var npcLocalKeys = map[string]string{
	"local_type": "local_type", "local_port": "local_port", "password": "password",
	"target": "target_addr", "target_addr": "target_addr", "target_type": "target_type",
	"fallback_secret": "fallback_secret", "local_proxy": "local_proxy",
}

// parseNPC reads npc://vkey@host:port?type=tls. Lists use the server and vkey
// query parameters; a local server parameter adds one local server.
func parseNPC(raw string, trusted bool) ([]Profile, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid npc URL") // the parse error would log the vkey
	}
	m := make(map[string]any)
	if host := u.Host + strings.TrimRight(u.Path, "/"); host != "" {
		m["server"] = host
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			return nil, errors.New("this npc URL has a newer format; upgrade npc to use it")
		}
		m["vkey"] = u.User.Username()
	}
	var local map[string]any
	for key, values := range u.Query() {
		value := strings.Join(values, ",")
		switch iniKey, isLocal := npcLocalKeys[key]; {
		case isLocal:
			if local == nil {
				local = map[string]any{"fallback_secret": true}
			}
			local[iniKey] = value
		case key == "server_addr":
			m["server"] = value
		case key == "conn_type":
			m["type"] = value
		case key == "proxy_url":
			m["proxy"] = value
		case key == "name" || shorthand[key] != "":
			m[key] = value
		default:
			return nil, fmt.Errorf("unknown npc URL parameter %q", key)
		}
	}
	if local != nil {
		m["locals"] = []any{local}
	}
	return parseProfile(m, "profile-1", trusted)
}
