// Package launch resolves what npc is told to start into one model, a list
// of Profile: launch links, configuration files, -L accesses and the flags
// and file forms of earlier versions (compat.go) all end there and run the
// same way.
//
// A connection is configuration text parsed by lib/config, so that package
// stays the only definition of the configuration keys.
package launch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/connprofile"
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
	// Hosting: besides its local accesses the connection runs as an
	// ordinary client, because its role says both or an access-less
	// writing shares it.
	Hosting bool
	// auto: Name was assigned, the source wrote none.
	auto bool
	// combined: more than one writing or a -L went into this connection.
	combined bool
	Config   *config.Config
	// Source says where the profile was written, for messages; it holds no secret.
	Source string
	// written names the session keys the source set itself. nil means the
	// form cannot tell: those different from the defaults count.
	written map[string]bool
	// aliases are further names of a connection written several times.
	aliases []string
	// by records which source wrote each session key, for messages.
	by map[string]string
	// writings records each time the connection is written: with its name
	// and whether an access goes with it, which decides its role.
	writings []writing
}

type writing struct {
	name   string
	access bool
}

var errNotLaunch = errors.New("不是启动链接、@文件或 base64，文件请写 @路径 (not a launch link, @file or base64; write a file as @path)")

// Resolve turns one -launch value into profiles.
func Resolve(ctx context.Context, input string) ([]Profile, error) {
	return resolve(ctx, input, true, 0)
}

// CheckValue checks the form of a -launch value without reading or fetching
// what it refers to.
func CheckValue(input string) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return errors.New("launch payload is empty")
	}
	if connprofile.IsLink(input) {
		_, err := connprofile.ParseLink(input)
		return err
	}
	if input[0] == '@' || isURL(input) {
		return nil
	}
	if _, ok := decodeBase64(input); !ok {
		return errNotLaunch
	}
	return nil
}

func isURL(s string) bool {
	if len(s) < 8 {
		return false
	}
	lower := strings.ToLower(s[:8])
	return strings.HasPrefix(lower, "http://") || lower == "https://"
}

func resolve(ctx context.Context, raw string, trusted bool, depth int) ([]Profile, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\xef\xbb\xbf"))
	switch {
	case raw == "":
		return nil, errors.New("launch payload is empty")
	case depth > maxDepth:
		return nil, errors.New("launch payload is nested too deeply")
	case (isURL(raw) || raw[0] == '@') && !trusted:
		return nil, errors.New("fetched content may not refer to a URL or file")
	case isURL(raw):
		body, err := fetch(ctx, raw)
		if err != nil {
			return nil, err
		}
		profiles, err := parseContent(ctx, body, false, depth+1)
		if err == nil {
			err = connectionsOnly(profiles)
		}
		if err != nil {
			return nil, &RemoteError{Err: err}
		}
		return profiles, nil
	case raw[0] == '@':
		body, err := readFile(strings.TrimSpace(raw[1:]))
		if err != nil {
			return nil, err
		}
		return parseContent(ctx, body, true, depth+1)
	case connprofile.IsLink(raw):
		return parseNPC(raw, trusted)
	}
	text, ok := decodeBase64(raw)
	if !ok {
		return nil, errNotLaunch
	}
	profiles, err := parseContent(ctx, text, trusted, depth+1)
	if err != nil && !structured(strings.TrimSpace(text)) {
		return nil, errNotLaunch
	}
	return profiles, err
}

// structured reports whether text is plainly meant as launch content, so
// that an error in it says more than "not base64".
func structured(text string) bool {
	return text != "" && (strings.ContainsRune("{[@/", rune(text[0])) || connprofile.IsLink(text) || isURL(text))
}

// parseContent reads the text of a file, a URL body or decoded base64:
// the configuration schema in JSON or JSONC, a single -launch value (an
// install keeps those in a file), or a form of earlier versions.
func parseContent(ctx context.Context, text string, trusted bool, depth int) ([]Profile, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\xef\xbb\xbf"))
	if text == "" {
		return nil, errors.New("launch content is empty")
	}
	if looksLikeINI(text) {
		return parseLegacyINI(text, trusted)
	}
	if text[0] == '{' || text[0] == '[' || strings.HasPrefix(text, "//") || strings.HasPrefix(text, "/*") {
		return parseJSON(text, trusted)
	}
	if connprofile.IsLink(text) || isURL(text) || text[0] == '@' {
		return resolve(ctx, text, trusted, depth)
	}
	if decoded, ok := decodeBase64(text); ok {
		return parseContent(ctx, decoded, trusted, depth+1)
	}
	return parseLegacyYAML(text, trusted)
}

// parseJSON reads JSON or JSONC: the schema of a configuration file, or
// the one of earlier versions.
func parseJSON(text string, trusted bool) ([]Profile, error) {
	plain := stripJSONC([]byte(text))
	if trimmed := bytes.TrimSpace(plain); len(trimmed) > 0 && trimmed[0] == '{' {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &keys); err != nil {
			return nil, fmt.Errorf("invalid launch JSON: %w", err)
		}
		if isDocument(keys) {
			return parseDocument(trimmed, trusted)
		}
	}
	return parseLegacyJSON(plain, trusted)
}

// URLAccess lets content fetched from a URL define access too
// (-launch_url_access). Only the local user sets it; the keys that reach
// local files stay refused either way.
var URLAccess bool

// connectionsOnly refuses fetched content defining anything but connections,
// and access where URLAccess allows it.
func connectionsOnly(profiles []Profile) error {
	for _, p := range profiles {
		c := p.Config
		if len(c.Hosts)+len(c.Tasks) > 0 {
			return errors.New("fetched content may not define hosts or tasks")
		}
		if len(c.LocalServer) > 0 && !URLAccess {
			return errors.New("fetched content may only define connections, not access: give the access locally with -L=...&via=name, or allow it with -launch_url_access=true")
		}
	}
	return nil
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

// decodeBase64 reads standard or URL-safe base64, padded or not, ignoring
// white space; decoded gzip is decompressed, up to maxPayloadSize.
func decodeBase64(raw string) (string, bool) {
	s := strings.Join(strings.Fields(raw), "")
	s = strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(s, "="))
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return "", false
	}
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return "", false
		}
		defer func() { _ = z.Close() }()
		if b, err = io.ReadAll(io.LimitReader(z, maxPayloadSize+1)); err != nil || len(b) > maxPayloadSize {
			return "", false
		}
	}
	if !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
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
	if cfg.UsesLegacyPointToPoint() {
		return nil, errors.New("point-to-point access settings of an earlier version (mode=p2p, [p2p...], target_type, fallback_secret, old local_type) are not accepted in launch payloads; use local_type and conn_mode")
	}
	c := cfg.CommonConfig
	if c.Server == "" || c.VKey == "" {
		return nil, errors.New("launch payload requires a server and a vkey")
	}
	c.Tp = strings.ToLower(c.Tp)
	if c.Role != "" {
		var err error
		if c.Role, err = connprofile.NormalizeRole(c.Role); err != nil {
			return nil, err
		}
	}
	if want != nil && (len(cfg.Hosts) != want["hosts"] || len(cfg.Tasks) != want["tasks"] ||
		len(cfg.LocalServer) != want["locals"]) {
		return nil, errors.New(`a section name changes its kind: "secret" and "p2p" prefixes are reserved`)
	}
	for _, l := range cfg.LocalServer {
		if l.Password == "" {
			return nil, errors.New("a local server requires a password")
		}
	}
	return []Profile{{Name: name, Direct: direct, Config: cfg}}, nil
}

// parseNPC reads a launch link (connprofile.ParseLink) as one direct profile.
func parseNPC(raw string, trusted bool) ([]Profile, error) {
	l, err := connprofile.ParseLink(raw)
	if err != nil {
		return nil, err
	}
	base := map[string]any{"server_addr": l.Server, "vkey": l.VKey}
	written := make(map[string]bool, len(l.Common))
	for k, v := range l.Common {
		base[k] = v
		written[k] = true
	}
	// the vkey and the server are written as they are: they are not lists
	var b strings.Builder
	if err := writeSection(&b, "common", base); err != nil {
		return nil, err
	}
	out, err := parseText(l.Name, b.String(), trusted, true, nil)
	if err != nil {
		return nil, err
	}
	out[0].written = written
	return out, nil
}
