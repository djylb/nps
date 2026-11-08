// Package connprofile parses the server address of one npc connection, a
// profile: one or more carrier candidates separated by "|". A candidate is
// a URL whose scheme names the carrier, or, without a scheme, the address
// syntax of earlier versions, addr:port@host:port/path, whose carrier is
// the profile's connection type. Separate profiles (a comma in -server)
// are split by the caller, not here.
//
// The URL form and its parameters are those of the carrier entries of the
// next major version (CARRIER with CARRIER_URL, CARRIER_SNI, CARRIER_KINDS,
// CARRIER_DIRS, TIER, WEIGHT, CARRIER_STANDBY, CARRIER_COUNT, CARRIER_ALPN,
// CARRIER_HTTP), so that a Candidate converts to one field by field. Only
// this version has the kcp, ws and wss schemes (ws and wss are http and
// https with kinds=websocket).
package connprofile

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/slogx"
)

var log = slogx.Module("config")

// Pipe modes of a profile (-pipes, npc.conf pipes=).
const (
	PipesSingle = "single"
	PipesMulti  = "multi"
)

// ParsePipes returns the pipe mode s names; "" is PipesSingle.
func ParsePipes(s string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(s)); m {
	case "", PipesSingle:
		return PipesSingle, nil
	case PipesMulti:
		return PipesMulti, nil
	default:
		return "", fmt.Errorf("pipes %q: want single or multi", s)
	}
}

// Dirs is the set of directions a candidate carries (CARRIER_DIRS).
type Dirs uint8

const (
	DirBidi Dirs = 1 // both directions; only these can start a session
	DirUp   Dirs = 2 // npc to server only
	DirDown Dirs = 4 // server to npc only
)

// KindWebSocket is the one HTTP carrier kind this version speaks.
const KindWebSocket = "websocket"

// Candidate is one way to reach the server of a profile.
type Candidate struct {
	// The fields of the carrier entry of the next major version.
	URL     string   // scheme://dial-address[/path], scheme tcp, tls, quic, http, https (kcp in this version only)
	Host    string   // Host header and default SNI, host:port
	SNI     string   // TLS server name, "" for none (an IP address)
	Kinds   []string // HTTP carrier kinds, for http and https: websocket
	Dirs    Dirs
	Tier    int // lower is tried first
	Weight  int // kept for multi pipes; unused by this version
	Standby bool
	Count   int // pipes in multi mode, at least 1

	ALPN []string // TLS ALPN of tls and quic, in order of preference
	HTTP []string // HTTP versions of http and https: http/1.1, h2, h3, in order of preference

	// How this version dials it.
	Carrier string // tcp, tls, kcp, quic, ws, wss (scheme-less: the profile type as given)
	Addr    string // host:port to dial
	Path    string // WebSocket path, escaped

	Index int    // position in the profile, from 0
	Raw   string // the candidate as written
}

// Key identifies the candidate within its profile.
func (c Candidate) Key() string { return c.Raw }

// Starts reports whether a session can start on c (it carries both
// directions).
func (c Candidate) Starts() bool { return c.Dirs&DirBidi != 0 }

// schemes maps a URL scheme to the carrier of this version and the scheme
// of the next major version.
var schemes = map[string][2]string{
	"tcp": {"tcp", "tcp"}, "tls": {"tls", "tls"}, "quic": {"quic", "quic"}, "kcp": {"kcp", "kcp"},
	"http": {"ws", "http"}, "https": {"wss", "https"}, "ws": {"ws", "http"}, "wss": {"wss", "https"},
}

var defaultPorts = map[string]string{
	"tcp": "8024", "kcp": "8024", "tls": "8025", "quic": "8025", "ws": "80", "wss": "443",
}

// EnsurePort adds the default port of carrier to addr when it has none.
func EnsurePort(addr, carrier string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return addr
	}
	p, ok := defaultPorts[carrier]
	if !ok {
		return addr
	}
	host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	return net.JoinHostPort(host, p)
}

// Parse returns the candidates of the profile s; carrier is the connection
// type of scheme-less candidates. Empty candidates are skipped. A "|" or
// "?" inside a path or parameter is written %7C or %3F.
func Parse(s, carrier string) ([]Candidate, error) {
	carrier = strings.ToLower(strings.TrimSpace(carrier))
	if carrier == "" {
		carrier = "tcp"
	}
	var out []Candidate
	for raw := range strings.SplitSeq(s, "|") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var (
			c   Candidate
			err error
		)
		if strings.Contains(raw, "://") {
			c, err = parseURL(raw)
		} else {
			c = parseLegacy(raw, carrier)
		}
		if err != nil {
			return nil, fmt.Errorf("server candidate %q: %w", raw, err)
		}
		c.Index, c.Raw = len(out), raw
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("server address is empty")
	}
	if !slices.ContainsFunc(out, Candidate.Starts) {
		return nil, errors.New("no candidate carries both directions: a session starts on one with dirs=bidi (the default)")
	}
	return out, nil
}

// parseLegacy reads addr:port@host:port/path the way earlier versions did:
// for quic the path names the ALPN.
func parseLegacy(s, carrier string) Candidate {
	c := Candidate{Carrier: carrier, Dirs: DirBidi, Count: 1}
	server, path := netaddr.SplitServerAndPath(s)
	if carrier == "quic" {
		c.ALPN = []string{DefaultQUICALPN}
	}
	if path == "" {
		path = "/ws"
	} else if carrier == "quic" {
		c.ALPN = []string{strings.TrimSpace(strings.TrimPrefix(path, "/"))}
	}
	c.Path = path
	addr, host, sni := netaddr.SplitAddrAndHost(server)
	c.Addr, c.Host, c.SNI = EnsurePort(addr, carrier), host, sni
	scheme := carrier
	switch carrier {
	case "ws", "wss":
		scheme = map[string]string{"ws": "http", "wss": "https"}[carrier]
		c.Kinds = []string{KindWebSocket}
		c.HTTP = defaultHTTP(scheme)
	default:
		path = ""
	}
	c.URL = entryURL(scheme, c.Addr, path)
	return c
}

// entryURL is the CARRIER_URL of a candidate: http and https leave out
// their default port, "/" and the default WebSocket path are no path.
func entryURL(scheme, addr, path string) string {
	if h, p, err := net.SplitHostPort(addr); err == nil {
		if scheme == "http" && p == "80" || scheme == "https" && p == "443" {
			addr = h
			if strings.Contains(h, ":") {
				addr = "[" + h + "]"
			}
		}
	}
	if path == "/" {
		path = ""
	}
	return scheme + "://" + strings.ToLower(addr) + path
}

func parseURL(s string) (Candidate, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Candidate{}, err
	}
	sc, ok := schemes[strings.ToLower(u.Scheme)]
	if !ok {
		return Candidate{}, fmt.Errorf("unknown scheme %q (want tcp, tls, quic, http, https, or kcp, ws, wss)", u.Scheme)
	}
	carrier, scheme := sc[0], sc[1]
	httpKind := scheme == "http" || scheme == "https"
	switch {
	case u.User != nil:
		return Candidate{}, errors.New("user information is not allowed")
	case u.Fragment != "":
		return Candidate{}, errors.New("a fragment is not allowed")
	case u.Hostname() == "":
		return Candidate{}, errors.New("no host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return Candidate{}, fmt.Errorf("invalid port %q", p)
		}
	}
	c := Candidate{Carrier: carrier, Dirs: DirBidi, Count: 1, Path: u.EscapedPath()}
	c.Addr = EnsurePort(u.Host, carrier)
	path := c.Path
	if path != "" && path != "/" && !httpKind {
		return Candidate{}, fmt.Errorf("a path is only used by http, https, ws and wss, not %s", u.Scheme)
	}
	if path == "" || !httpKind {
		path, c.Path = "", "/ws"
	}
	if httpKind {
		c.Kinds = []string{KindWebSocket}
		c.HTTP = defaultHTTP(scheme)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Candidate{}, err
	}
	host, sni, hasSNI := u.Host, "", false
	for key, values := range q {
		if len(values) > 1 {
			return Candidate{}, fmt.Errorf("parameter %q given twice", key)
		}
		v := strings.TrimSpace(values[0])
		switch key {
		case "tier":
			if c.Tier, err = strconv.Atoi(v); err != nil {
				return Candidate{}, fmt.Errorf("tier %q is not an integer", v)
			}
		case "weight":
			if c.Weight, err = strconv.Atoi(v); err != nil || c.Weight < 0 {
				return Candidate{}, fmt.Errorf("weight %q is not a non-negative integer", v)
			}
		case "standby":
			if v == "" {
				c.Standby = true
			} else if c.Standby, err = strconv.ParseBool(v); err != nil {
				return Candidate{}, fmt.Errorf("standby %q is not a boolean", v)
			}
		case "count":
			if c.Count, err = strconv.Atoi(v); err != nil || c.Count < 1 {
				return Candidate{}, fmt.Errorf("count %q is not a positive integer", v)
			}
		case "dirs":
			c.Dirs = 0
			for d := range strings.SplitSeq(v, ",") {
				switch strings.ToLower(strings.TrimSpace(d)) {
				case "bidi":
					c.Dirs |= DirBidi
				case "up":
					c.Dirs |= DirUp
				case "down":
					c.Dirs |= DirDown
				default:
					return Candidate{}, fmt.Errorf("dirs %q: want bidi, up, down or a comma list of them", v)
				}
			}
		case "kinds":
			if !httpKind {
				return Candidate{}, fmt.Errorf("kinds is only used by http and https, not %s", u.Scheme)
			}
			var ignored []string
			for k := range strings.SplitSeq(v, ",") {
				if k = strings.ToLower(strings.TrimSpace(k)); k != KindWebSocket && k != "" {
					ignored = append(ignored, k)
				}
			}
			if len(ignored) > 0 {
				warnOnce(s, "carrier kinds other than websocket need zmux; ignored", "kinds", strings.Join(ignored, ","))
			}
		case "host":
			if v == "" {
				return Candidate{}, errors.New("host is empty")
			}
			host = v
			if _, _, err := net.SplitHostPort(v); err != nil {
				if p := u.Port(); p != "" {
					host = net.JoinHostPort(strings.Trim(v, "[]"), p)
				}
			}
		case "sni":
			sni, hasSNI = v, true
		case "alpn":
			if carrier != "tls" && carrier != "quic" {
				return Candidate{}, fmt.Errorf("alpn is used by tls and quic, not %s (http and https take http=)", u.Scheme)
			}
			if c.ALPN = list(v); len(c.ALPN) == 0 {
				return Candidate{}, errors.New("alpn is empty")
			}
		case "http":
			if !httpKind {
				return Candidate{}, fmt.Errorf("http is used by http, https, ws and wss, not %s", u.Scheme)
			}
			if c.HTTP, err = httpVersions(v, u.Scheme); err != nil {
				return Candidate{}, err
			}
		default:
			return Candidate{}, fmt.Errorf("unknown parameter %q (want tier, weight, standby, count, dirs, kinds, host, sni, alpn or http)", key)
		}
	}
	c.Host = host
	if hasSNI {
		c.SNI = sni
	} else {
		_, _, c.SNI = netaddr.SplitAddrAndHost(host)
	}
	if len(c.ALPN) == 0 && carrier == "quic" {
		c.ALPN = []string{DefaultQUICALPN}
	}
	if httpKind && !slices.Contains(c.HTTP, HTTP11) {
		warnOnce(s, "http versions other than http/1.1 need zmux; using http/1.1", "http", strings.Join(c.HTTP, ","))
	}
	c.URL = entryURL(scheme, c.Addr, path)
	return c, nil
}

// DefaultQUICALPN is the ALPN of quic carriers of this version.
const DefaultQUICALPN = "nps"

// HTTP versions of http and https carriers.
const (
	HTTP11 = "http/1.1"
	HTTP2  = "h2"
	HTTP3  = "h3"
)

// defaultHTTP is the HTTP version list of scheme when none is given.
func defaultHTTP(scheme string) []string {
	if scheme == "https" {
		return []string{HTTP2, HTTP11}
	}
	return []string{HTTP11}
}

// httpVersions reads an http= list. WebSocket schemes (ws, wss) run over
// http/1.1 and h2 only.
func httpVersions(v, scheme string) ([]string, error) {
	out := list(strings.ToLower(v))
	if len(out) == 0 {
		return nil, errors.New("http is empty")
	}
	ws := scheme == "ws" || scheme == "wss"
	for i, h := range out {
		switch h {
		case HTTP11, HTTP2:
		case HTTP3:
			if ws {
				return nil, fmt.Errorf("http=h3 is not used by %s (websocket runs over http/1.1 and h2)", scheme)
			}
		default:
			return nil, fmt.Errorf("http %q: want http/1.1, h2, h3 or a comma list of them", h)
		}
		if slices.Contains(out[:i], h) {
			return nil, fmt.Errorf("http %q given twice", h)
		}
	}
	return out, nil
}

// list splits a comma list, dropping empty items.
func list(v string) []string {
	var out []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var warned sync.Map

// warnOnce logs msg once per candidate text.
func warnOnce(candidate, msg string, args ...any) {
	if _, seen := warned.LoadOrStore(candidate+"\x00"+msg, true); !seen {
		log.Warn(msg, append([]any{"candidate", candidate}, args...)...)
	}
}

// MultiMembers returns the candidates that carry pipes in multi mode, in the
// order the pools are built: the non-standby ones with a tier at most the
// best non-standby tier + 1, as written, then every standby one. Standby
// pipes are built but carry traffic only while no other pipe is alive.
func MultiMembers(cands []Candidate) []Candidate {
	best, have := 0, false
	for _, c := range cands {
		if !c.Standby && (!have || c.Tier < best) {
			best, have = c.Tier, true
		}
	}
	var out, standby []Candidate
	for _, c := range cands {
		switch {
		case c.Standby:
			standby = append(standby, c)
		case c.Tier <= best+1:
			out = append(out, c)
		}
	}
	return append(out, standby...)
}

// Order returns the candidates in the order to try them: the one with key
// last (the last that connected) first, then the others by tier ascending,
// then as written, standby candidates after all others.
func Order(cands []Candidate, last string) []Candidate {
	out := slices.Clone(cands)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		if al, bl := last != "" && a.Key() == last, last != "" && b.Key() == last; al != bl {
			if al {
				return -1
			}
			return 1
		}
		if a.Standby != b.Standby {
			if a.Standby {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Tier, b.Tier)
	})
	return out
}
