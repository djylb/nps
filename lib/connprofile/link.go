package connprofile

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// MaxLinkSize is the longest launch link text.
const MaxLinkSize = 8 << 10

// LinkScheme is the scheme of a launch link.
const LinkScheme = "npc://"

// IsLink reports whether s is written as a launch link.
func IsLink(s string) bool {
	return len(s) >= len(LinkScheme) && strings.EqualFold(s[:len(LinkScheme)], LinkScheme)
}

// Link is a launch link, npc://vkey@host:port[/path]?type=tls&...#label,
// read as the settings of one connection profile. Its keys are those of
// npc.conf, and the server is in the candidate form of Parse, so a link is
// only another way to write what -server, -vkey and npc.conf say.
type Link struct {
	VKey   string
	Server string // candidates joined by "|", for Parse
	// Name is the #label, else the name parameter: the profile name.
	Name string
	// Common holds the other [common] keys of the link: pipes, proxy_url,
	// local_ip, new_protocol_pin and the core settings.
	Common map[string]string
	// Local holds the keys of one local server (point-to-point visitor):
	// password, target_addr, local_type, local_port, conn_mode, local_proxy.
	// It is empty for a link that connects only.
	Local map[string]string
}

// Link types: those of a candidate that this version dials.
var linkTypes = []string{"tcp", "tls", "ws", "wss", "quic"}

// candidateKeys are the link parameters that belong to the first address;
// they have the names of the candidate parameters of Parse.
var candidateKeys = []string{"host", "sni", "alpn", "http", "subprotocol", "tier", "weight", "standby", "count", "dirs", "kinds"}

// commonKeys maps link parameters to [common] keys.
var commonKeys = map[string]string{
	"pipes": "pipes", "scheduler_up": "scheduler_up", "session_max_lanes": "session_max_lanes",
	"relay_idle": "relay_idle", "p2p_visitor_keep_server": "p2p_visitor_keep_server",
	"session_resume_timeout": "session_resume_timeout", "proxy": "proxy_url", "proxy_url": "proxy_url",
	"local_ip": "local_ip", "pin": "new_protocol_pin",
}

// localKeys maps link parameters to local server keys; access is the
// spelling of the key password.
var localKeys = map[string]string{
	"access": "password", "password": "password", "target": "target_addr", "target_addr": "target_addr",
	"local_type": "local_type", "local_port": "local_port", "conn_mode": "conn_mode", "local_proxy": "local_proxy",
}

// ParseLink reads a launch link. Errors never contain the link: it holds
// the vkey. A parameter this version does not know is ignored with a
// warning, so a link written for a newer version still connects.
func ParseLink(raw string) (Link, error) {
	var l Link
	raw = strings.TrimSpace(raw)
	switch {
	case !IsLink(raw):
		return l, errors.New("a launch link starts with npc://")
	case len(raw) > MaxLinkSize:
		return l, fmt.Errorf("launch link is longer than %d bytes", MaxLinkSize)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return l, errors.New("invalid launch link") // the parse error would show the vkey
	}
	switch {
	case u.User == nil || u.User.Username() == "":
		return l, errors.New("after npc:// there is neither vkey@host nor valid base64: write npc://<vkey>@host:port")
	case u.Hostname() == "":
		return l, errors.New("launch link has no server address")
	}
	if _, ok := u.User.Password(); ok {
		return l, errors.New("launch link: write a ':' in the vkey as %3A (a client number is not part of a link)")
	}
	l.VKey = u.User.Username()
	q, err := parseLinkQuery(u.RawQuery)
	if err != nil {
		return l, errors.New("invalid launch link query")
	}
	for key, values := range q {
		if len(values) > 1 && key != "alt" {
			return l, fmt.Errorf("launch link: parameter %q given twice", key)
		}
	}

	typ := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if typ == "" {
		typ = "tcp"
	}
	if !slices.Contains(linkTypes, typ) {
		return l, fmt.Errorf("launch link: type %q is not supported by this version (want %s)", typ, strings.Join(linkTypes, ", "))
	}
	cand := make(url.Values)
	for _, key := range candidateKeys {
		if v, ok := q[key]; ok {
			cand.Set(key, v[0])
		}
	}
	first := typ + "://" + u.Host + strings.ReplaceAll(strings.ReplaceAll(u.EscapedPath(), ";", "%3B"), "|", "%7C")
	if len(cand) > 0 {
		first += "?" + cand.Encode()
	}
	cands := []string{first}
	for _, alt := range q["alt"] {
		c, err := altCandidate(alt)
		if err != nil {
			return l, err
		}
		cands = append(cands, c)
	}
	l.Server = strings.Join(cands, "|")
	if _, err := Parse(l.Server, ""); err != nil {
		return l, err
	}

	l.Common, l.Local = map[string]string{}, map[string]string{}
	local := make(map[string]string) // link key that set each local key
	for _, key := range sortedKeys(q) {
		v := strings.TrimSpace(q[key][0])
		switch ck, isCommon := commonKeys[key]; {
		case key == "type" || key == "alt" || slices.Contains(candidateKeys, key):
		case key == "name":
			l.Name = v
		case isCommon:
			if ck == "new_protocol_pin" {
				if b, err := hex.DecodeString(v); err != nil || len(b) != 16 {
					return l, errors.New("launch link: pin is 32 hex digits")
				}
			}
			l.Common[ck] = v
		case localKeys[key] != "":
			lk := localKeys[key]
			if prev, dup := local[lk]; dup {
				return l, fmt.Errorf("launch link: %s and %s name the same setting", prev, key)
			}
			local[lk] = key
			l.Local[lk] = v
		default:
			log.Warn("launch link parameter ignored", "param", key)
		}
	}
	if label := u.Fragment; label != "" {
		l.Name = label
	}
	if len(l.Local) > 0 && l.Local["password"] == "" {
		return l, errors.New("launch link: a local server needs access (the key of the tunnel)")
	}
	return l, nil
}

// parseLinkQuery splits a link query at "&" and each pair at its first "=",
// then decodes both parts. url.ParseQuery is not used: it rejects a ";",
// which an alt value uses to separate its own parameters.
func parseLinkQuery(raw string) (url.Values, error) {
	q := make(url.Values)
	for pair := range strings.SplitSeq(raw, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		k, err := url.QueryUnescape(k)
		if err != nil {
			return nil, err
		}
		if v, err = url.QueryUnescape(v); err != nil {
			return nil, err
		}
		q[k] = append(q[k], v)
	}
	return q, nil
}

func sortedKeys(q url.Values) []string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// altCandidate turns an alt value, type://addr:port[@host[:port]][/path]
// with an optional query of candidate parameters (k=v;k=v), into the candidate form
// of Parse: the part after "@" becomes the host parameter.
func altCandidate(alt string) (string, error) {
	alt = strings.TrimSpace(alt)
	scheme, rest, ok := strings.Cut(alt, "://")
	scheme = strings.ToLower(scheme)
	if !ok || !slices.Contains(linkTypes, scheme) {
		return "", fmt.Errorf("launch link: alt %q must start with %s://", alt, strings.Join(linkTypes, "://, "))
	}
	rest, query, _ := strings.Cut(rest, "?")
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	addr, host, hasHost := strings.Cut(authority, "@")
	if addr == "" {
		return "", fmt.Errorf("launch link: alt %q has no address", alt)
	}
	// the alt's own parameters are separated by ";" (or by an encoded "&")
	vals, err := url.ParseQuery(strings.ReplaceAll(query, ";", "&"))
	if err != nil {
		return "", fmt.Errorf("launch link: alt %q: invalid query", alt)
	}
	if hasHost {
		if vals.Has("host") {
			return "", fmt.Errorf("launch link: alt %q gives host twice", alt)
		}
		vals.Set("host", host)
	}
	out := scheme + "://" + addr + strings.ReplaceAll(strings.ReplaceAll(path, ";", "%3B"), "|", "%7C")
	if len(vals) > 0 {
		out += "?" + vals.Encode()
	}
	return out, nil
}

// AppendQuery adds the parameters vals to the query of link, before its
// fragment. A parameter the link already has makes ParseLink fail, so a
// value on the command line never silently overrides the link or the other
// way round.
func AppendQuery(link string, vals url.Values) string {
	if len(vals) == 0 {
		return link
	}
	body, frag, hasFrag := strings.Cut(link, "#")
	sep := "?"
	if strings.Contains(body, "?") {
		sep = "&"
	}
	body += sep + vals.Encode()
	if hasFrag {
		body += "#" + frag
	}
	return body
}
