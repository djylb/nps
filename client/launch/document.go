package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// path is one way to reach the server in a configuration file.
type path struct {
	URL         string `json:"url"`
	ALPN        string `json:"alpn"`
	SNI         string `json:"sni"`
	Host        string `json:"host"`
	HTTP        string `json:"http"`
	Subprotocol string `json:"subprotocol"`
	Tier        int    `json:"tier"`
	Weight      int    `json:"weight"`
	Standby     bool   `json:"standby"`
	Dirs        string `json:"dirs"`
	Count       int    `json:"count"`
	Kinds       string `json:"kinds"`
}

// candidate writes p in the candidate form of connprofile.Parse.
func (p path) candidate() (string, error) {
	if !strings.Contains(p.URL, "://") {
		return "", fmt.Errorf("path url %q needs a scheme, such as tls://host:port", p.URL)
	}
	q := make(url.Values)
	for k, v := range map[string]string{"alpn": p.ALPN, "sni": p.SNI, "host": p.Host, "http": p.HTTP,
		"subprotocol": p.Subprotocol, "dirs": p.Dirs, "kinds": p.Kinds} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for k, v := range map[string]int{"tier": p.Tier, "weight": p.Weight, "count": p.Count} {
		if v != 0 {
			q.Set(k, strconv.Itoa(v))
		}
	}
	if p.Standby {
		q.Set("standby", "true")
	}
	if len(q) == 0 {
		return p.URL, nil
	}
	sep := "?"
	if strings.Contains(p.URL, "?") {
		sep = "&"
	}
	return p.URL + sep + q.Encode(), nil
}

type session struct {
	MaxPipes      int    `json:"max_pipes"`
	SchedulerUp   string `json:"scheduler_up"`
	MaxLanes      int    `json:"max_lanes"`
	ResumeTimeout int    `json:"resume_timeout"`
	UsageInterval int    `json:"usage_interval"`
}

// common is the session as [common] keys, only those written.
func (s *session) common() (map[string]any, error) {
	out := make(map[string]any)
	switch {
	case s == nil:
		return out, nil
	case s.MaxPipes < 0:
		return nil, errors.New("session: max_pipes is negative")
	case s.MaxPipes == 1:
		out["pipes"] = "single"
	case s.MaxPipes > 1:
		out["pipes"] = "multi"
	}
	if s.SchedulerUp != "" {
		out["scheduler_up"] = s.SchedulerUp
	}
	if s.MaxLanes != 0 {
		out["session_max_lanes"] = s.MaxLanes
	}
	if s.ResumeTimeout != 0 {
		out["session_resume_timeout"] = s.ResumeTimeout
	}
	if s.UsageInterval != 0 {
		out["session_usage_interval"] = s.UsageInterval
	}
	return out, nil
}

// connection is one server instance in a configuration file. In "profiles"
// its name is the key; Name is only written by a single instance at the top
// level.
type connection struct {
	Name       string   `json:"name"`
	VKey       string   `json:"vkey"`
	SourceAddr string   `json:"source_addr"`
	Resident   bool     `json:"resident"`
	Paths      []path   `json:"paths"`
	Session    *session `json:"session"`
	Access     []Access `json:"access"`
}

func (c connection) profile(trusted bool) (Profile, error) {
	var p Profile
	switch {
	case c.VKey == "":
		return p, errors.New("vkey is required")
	case len(c.Paths) == 0:
		return p, errors.New("at least one path is required")
	}
	cands := make([]string, len(c.Paths))
	for i, path := range c.Paths {
		var err error
		if cands[i], err = path.candidate(); err != nil {
			return p, fmt.Errorf("paths[%d]: %w", i, err)
		}
	}
	base, err := c.Session.common()
	if err != nil {
		return p, err
	}
	if c.SourceAddr != "" {
		base["local_ip"] = c.SourceAddr
	}
	if c.Resident {
		base["resident"] = true
	}
	written := make(map[string]bool, len(base))
	for k := range base {
		written[k] = true
	}
	base["server_addr"], base["vkey"] = strings.Join(cands, "|"), c.VKey
	var b strings.Builder
	if err := writeSection(&b, "common", base); err != nil {
		return p, err
	}
	out, err := parseText(c.Name, b.String(), trusted, true, nil)
	if err != nil {
		return p, err
	}
	p = out[0]
	p.written = written
	for i, a := range c.Access {
		l, err := a.compile()
		if err != nil {
			return p, fmt.Errorf("access[%d]: %w", i, err)
		}
		p.Config.LocalServer = append(p.Config.LocalServer, l)
		p.Direct = false
	}
	return p, nil
}

// isDocument reports whether the keys are those of the configuration file
// schema: "profiles" as an object, or one instance at the top level. A
// "profiles" list is the file of an earlier version (compat.go).
func isDocument(keys map[string]json.RawMessage) bool {
	_, paths := keys["paths"]
	return paths || isObject(keys["profiles"])
}

func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

// parseDocument reads the configuration file schema from JSON text.
func parseDocument(text []byte, trusted bool) ([]Profile, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(text, &keys); err != nil {
		return nil, fmt.Errorf("invalid launch JSON: %w", err)
	}
	var doc struct {
		Profiles map[string]connection `json:"profiles"`
	}
	if _, ok := keys["profiles"]; ok {
		if len(keys) != 1 {
			return nil, errors.New(`"profiles" cannot be combined with other keys`)
		}
		if err := decodeStrict(text, &doc); err != nil {
			return nil, err
		}
		if len(doc.Profiles) == 0 {
			return nil, errors.New("launch content has no profiles")
		}
	} else {
		var c connection
		if err := decodeStrict(text, &c); err != nil {
			return nil, err
		}
		doc.Profiles = map[string]connection{c.Name: c}
	}
	out := make([]Profile, 0, len(doc.Profiles))
	for _, name := range slices.Sorted(maps.Keys(doc.Profiles)) {
		c := doc.Profiles[name]
		if _, ok := keys["profiles"]; ok {
			switch {
			case name == "":
				return nil, errors.New("a profile needs a name")
			case c.Name != "":
				return nil, fmt.Errorf("profile %q: the key is its name, remove \"name\"", name)
			}
			c.Name = name
		}
		p, err := c.profile(trusted)
		if err != nil {
			if name == "" {
				return nil, err
			}
			return nil, fmt.Errorf("profile %q: %w", name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func decodeStrict(text []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid launch content: %w", err)
	}
	return nil
}

// stripJSONC removes comments and trailing commas outside strings, leaving
// plain JSON. Comments become spaces, so error offsets stay meaningful.
func stripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '"':
			start := i
			for i++; i < len(in) && in[i] != '"'; i++ {
				if in[i] == '\\' {
					i++
				}
			}
			out = append(out, in[start:min(i+1, len(in))]...)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			for i += 2; i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/'); i++ {
			}
			i++
		case c == '}' || c == ']':
			if t := bytes.TrimRight(out, " \t\r\n"); len(t) > 0 && t[len(t)-1] == ',' {
				out = t[:len(t)-1]
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
