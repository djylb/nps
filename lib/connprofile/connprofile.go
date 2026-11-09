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

// Sending schedulers of a session (§5 scheduler_up, scheduler_down), indexed
// by their value on the wire.
const (
	SchedulerAffinity           = "affinity"
	SchedulerSessionAffinity    = "session_affinity"
	SchedulerSpreadETA          = "spread_eta"
	SchedulerSpreadRoundRobin   = "spread_round_robin"
	SchedulerSpreadRateWeighted = "spread_rate_weighted"
)

// Schedulers lists the schedulers by wire value.
var Schedulers = []string{SchedulerAffinity, SchedulerSessionAffinity, SchedulerSpreadETA, SchedulerSpreadRoundRobin, SchedulerSpreadRateWeighted}

// ParseScheduler returns the scheduler s names; "" is affinity.
func ParseScheduler(s string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(s))
	if m == "" {
		return SchedulerAffinity, nil
	}
	if !slices.Contains(Schedulers, m) {
		return "", fmt.Errorf("scheduler %q: want one of %s", s, strings.Join(Schedulers, ", "))
	}
	return m, nil
}

// Dirs is the set of directions a candidate carries (CARRIER_DIRS).
type Dirs uint8

const (
	DirBidi Dirs = 1 // both directions; only these can start a session
	DirUp   Dirs = 2 // npc to server only
	DirDown Dirs = 4 // server to npc only
)

// HTTP carrier kinds of http and https candidates (zmux CarrierKind names).
const (
	KindWebSocket = "websocket"
	KindStreaming = "streaming"
	KindSplit     = "split"
	KindHTTP3     = "http3"
)

// httpKinds are the kinds an http or https candidate tries, in order, when
// it names none. An http list with h3 adds http3
// (candidateKinds).
var httpKinds = []string{KindWebSocket, KindStreaming, KindSplit}

// knownKinds are the kinds a kinds parameter may name on http and https.
var knownKinds = append(slices.Clone(httpKinds), KindHTTP3)

// Candidate is one way to reach the server of a profile.
type Candidate struct {
	// The fields of the carrier entry of the next major version.
	URL     string   // scheme://dial-address[/path], scheme tcp, tls, quic, http, https (kcp in this version only)
	Host    string   // Host header and default SNI, host:port
	SNI     string   // TLS server name, "" for none (an IP address)
	Kinds   []string // HTTP carrier kinds of http and https, in order of preference
	Dirs    Dirs
	Tier    int // lower is tried first
	Weight  int // share of a pipe in multi mode
	Standby bool
	Count   int // pipes in multi mode, at least 1

	ALPN []string // TLS ALPN of tls and quic, in order of preference
	// Subprotocol is the WebSocket subprotocol of ws and wss, "" for none.
	Subprotocol string

	HTTP []string // HTTP versions of http and https: http/1.1, h2, h3, in order of preference

	// How this version dials it.
	Carrier string // tcp, tls, kcp, quic, ws, wss, http, https (scheme-less: the profile type as given)
	Addr    string // host:port to dial
	Path    string // HTTP path, escaped

	// ALPNDefaulted: no ALPN was written for this quic candidate.
	ALPNDefaulted bool

	Index int    // position in the profile, from 0
	Raw   string // the candidate as written

	// Attempt names one (HTTP version, kind) of an http or https candidate
	// (Attempts), "" for the candidate as written.
	Attempt string
}

// Key identifies the candidate, or one of its attempts, within its profile.
func (c Candidate) Key() string { return c.Raw + c.Attempt }

// HTTPCarrier reports whether c is an http or https candidate, which chooses
// among carrier kinds.
func (c Candidate) HTTPCarrier() bool { return c.Carrier == "http" || c.Carrier == "https" }

// Attempts returns the ways to dial c: one per (HTTP version, kind) it can
// carry, the versions in the order of its http list and the kinds in the
// order of its kinds within each. Each is c with one version and one kind; a
// websocket attempt is dialed as ws or wss. Any other candidate is its own
// only attempt.
func (c Candidate) Attempts() []Candidate {
	if !c.HTTPCarrier() {
		return []Candidate{c}
	}
	var out []Candidate
	for _, v := range c.HTTP {
		if !httpVersionDialable(v, c.Carrier) {
			continue
		}
		for _, k := range c.Kinds {
			if !kindCarried(k, v) {
				continue
			}
			a := c
			a.HTTP, a.Kinds, a.Attempt = []string{v}, []string{k}, "#"+v+"/"+k
			if k == KindWebSocket {
				a.Carrier = map[string]string{"http": "ws", "https": "wss"}[c.Carrier]
			}
			out = append(out, a)
		}
	}
	return out
}

// httpVersionDialable reports whether this version dials v on carrier: h2
// and h3 need TLS.
func httpVersionDialable(v, carrier string) bool {
	return v == HTTP11 || (v == HTTP2 || v == HTTP3) && carrier == "https"
}

// kindCarried reports whether version v carries kind k (§2.4b): http3 only
// runs over h3, nothing else does, and WebSocket is dialed over HTTP/1.1.
func kindCarried(k, v string) bool {
	switch k {
	case KindHTTP3:
		return v == HTTP3
	case KindWebSocket:
		return v == HTTP11
	}
	return v != HTTP3
}

// OnlyHTTP3 reports whether h3 is all the http list of c names (§2.4b): only
// then is h3 dialed without being known to work.
func (c Candidate) OnlyHTTP3() bool { return slices.Equal(c.HTTP, []string{HTTP3}) }

// Attempts returns the attempts of every candidate, in order.
func Attempts(cands []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cands {
		out = append(out, c.Attempts()...)
	}
	return out
}

// Starts reports whether a session can start on c (it carries both
// directions).
func (c Candidate) Starts() bool { return c.Dirs&DirBidi != 0 }

// schemes maps a URL scheme to the carrier of this version and the scheme
// of the next major version.
var schemes = map[string][2]string{
	"tcp": {"tcp", "tcp"}, "tls": {"tls", "tls"}, "quic": {"quic", "quic"}, "kcp": {"kcp", "kcp"},
	"http": {"http", "http"}, "https": {"https", "https"}, "ws": {"ws", "http"}, "wss": {"wss", "https"},
}

var defaultPorts = map[string]string{
	"tcp": "8024", "kcp": "8024", "tls": "8025", "quic": "8025", "ws": "80", "wss": "443", "http": "80", "https": "443",
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
		c.ALPN, c.ALPNDefaulted = []string{DefaultQUICALPN}, true
	}
	if path == "" {
		path = "/ws"
	} else if carrier == "quic" {
		c.ALPN, c.ALPNDefaulted = []string{strings.TrimSpace(strings.TrimPrefix(path, "/"))}, false
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
	isWS := carrier == "ws" || carrier == "wss"
	if httpKind {
		c.Kinds = []string{KindWebSocket}
		if !isWS {
			c.Kinds = slices.Clone(httpKinds)
		}
		c.HTTP = defaultHTTP(scheme)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Candidate{}, err
	}
	host, sni, hasSNI := u.Host, "", false
	kindsWritten := q.Has("kinds")
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
			var ignored, kinds []string
			for k := range strings.SplitSeq(v, ",") {
				k = strings.ToLower(strings.TrimSpace(k))
				switch {
				case k == "":
				case slices.Contains(kinds, k):
					return Candidate{}, fmt.Errorf("kind %q given twice", k)
				case k == KindWebSocket || !isWS && slices.Contains(knownKinds, k):
					kinds = append(kinds, k)
				default:
					ignored = append(ignored, k)
				}
			}
			if len(ignored) > 0 {
				warnOnce(s, "carrier kinds this version does not dial; ignored", "kinds", strings.Join(ignored, ","))
			}
			if len(kinds) > 0 {
				c.Kinds = kinds
			} else if !isWS {
				return Candidate{}, fmt.Errorf("kinds %q: want a comma list of %s", v, strings.Join(httpKinds, ", "))
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
		case "subprotocol":
			if !httpKind {
				return Candidate{}, fmt.Errorf("subprotocol is used by http, https, ws and wss, not %s", u.Scheme)
			}
			if v == "" {
				return Candidate{}, errors.New("subprotocol is empty")
			}
			c.Subprotocol = v
		case "http":
			if !httpKind {
				return Candidate{}, fmt.Errorf("http is used by http, https, ws and wss, not %s", u.Scheme)
			}
			if c.HTTP, err = httpVersions(v, u.Scheme); err != nil {
				return Candidate{}, err
			}
		default:
			return Candidate{}, fmt.Errorf("unknown parameter %q (want tier, weight, standby, count, dirs, kinds, host, sni, alpn, http or subprotocol)", key)
		}
	}
	if !isWS && !kindsWritten && slices.Contains(c.HTTP, HTTP3) {
		c.Kinds = append(slices.Clone(c.Kinds), KindHTTP3)
	}
	c.Host = host
	if hasSNI {
		c.SNI = sni
	} else {
		_, _, c.SNI = netaddr.SplitAddrAndHost(host)
	}
	if len(c.ALPN) == 0 && carrier == "quic" {
		c.ALPN, c.ALPNDefaulted = []string{DefaultQUICALPN}, true
	}
	if isWS && !slices.Contains(c.HTTP, HTTP11) {
		warnOnce(s, "websocket runs over http/1.1 here; using http/1.1", "http", strings.Join(c.HTTP, ","))
	}
	if c.HTTPCarrier() {
		if c.Dirs != DirBidi {
			return Candidate{}, fmt.Errorf("dirs up and down are not available on %s, which is not Bound", u.Scheme)
		}
		if len(c.Attempts()) == 0 {
			return Candidate{}, fmt.Errorf("no http version of %s can carry its kinds (h2 and h3 need https, http3 needs h3)", strings.Join(c.HTTP, ","))
		}
	}
	c.URL = entryURL(scheme, c.Addr, path)
	return c, nil
}

// DefaultQUICALPN is the ALPN of quic carriers (§13).
const DefaultQUICALPN = "h3"

// UseCoreDefaults gives cands the defaults of the new protocol.
func UseCoreDefaults(cands []Candidate) {
	for i := range cands {
		if cands[i].Carrier == "kcp" {
			// Not a carrier of the core protocol; the KCP port of a bridge
			// also takes QUIC.
			log.Warn("kcp is not available, connecting with quic", "server", cands[i].Addr)
			cands[i].Carrier, cands[i].ALPN = "quic", []string{DefaultQUICALPN}
		}
	}
}

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

// Separators of a server list: earlier versions separate connections with
// a comma; once any connection uses the URL form, connections are
// separated by a semicolon and commas belong to parameter lists.
const (
	SepLegacy = ","
	SepURL    = ";"
)

// ListSep returns the separator of the connections in server, and so of
// the lists that go with it (vkey, type, local_ip).
func ListSep(server string) string {
	if strings.Contains(server, "://") {
		return SepURL
	}
	return SepLegacy
}

// SplitList splits a list of server, vkey, type or local_ip values by sep,
// accepting the full-width forms of "," and ";" and dropping empty items.
func SplitList(s, sep string) []string {
	s = strings.NewReplacer("，", ",", "；", ";").Replace(s)
	var out []string
	for v := range strings.SplitSeq(s, sep) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// CheckList checks the connections of server: each must parse, and a URL
// list that looks like comma-separated connections gets a hint to use ";".
func CheckList(server string, types []string) error {
	sep := ListSep(server)
	for i, p := range SplitList(server, sep) {
		tp := ""
		if i < len(types) {
			tp = types[i]
		}
		if _, err := Parse(p, tp); err != nil {
			if sep == SepURL && looksLikeCommaList(p) {
				return fmt.Errorf("%w (separate connections with %q when the URL form is used; commas are for parameter lists)", err, SepURL)
			}
			return err
		}
	}
	return nil
}

// looksLikeCommaList reports whether p has a comma followed by what starts
// another candidate (a scheme or an address), outside a query.
func looksLikeCommaList(p string) bool {
	for i := strings.IndexByte(p, ','); i >= 0; i = strings.IndexByte(p, ',') {
		rest := p[i+1:]
		if strings.Contains(strings.SplitN(rest, "?", 2)[0], "://") || strings.Contains(strings.SplitN(rest, "|", 2)[0], ":") && !strings.Contains(p[:i], "?") {
			return true
		}
		p = rest
	}
	return false
}

// UsesType reports whether a server list has a candidate without a scheme,
// whose carrier comes from the connection type (-type, conn_type).
func UsesType(server string) bool {
	for _, p := range SplitList(server, ListSep(server)) {
		for c := range strings.SplitSeq(p, "|") {
			if c = strings.TrimSpace(c); c != "" && !strings.Contains(c, "://") {
				return true
			}
		}
	}
	return false
}
