package connprofile

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

var ws = []string{KindWebSocket}

func TestParseSchemes(t *testing.T) {
	cases := []struct {
		in   string
		want Candidate
	}{
		{"tcp://a.com:8024", Candidate{URL: "tcp://a.com:8024", Carrier: "tcp", Addr: "a.com:8024", Host: "a.com:8024", SNI: "a.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"tls://A.com", Candidate{URL: "tls://a.com:8025", Carrier: "tls", Addr: "A.com:8025", Host: "A.com", SNI: "A.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"tls://a.com?alpn=h2", Candidate{URL: "tls://a.com:8025", Carrier: "tls", Addr: "a.com:8025", Host: "a.com", SNI: "a.com", Path: "/ws", ALPN: []string{"h2"}, Dirs: DirBidi, Count: 1}},
		{"kcp://1.2.3.4", Candidate{URL: "kcp://1.2.3.4:8024", Carrier: "kcp", Addr: "1.2.3.4:8024", Host: "1.2.3.4", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"quic://1.2.3.4:9?sni=b.com", Candidate{URL: "quic://1.2.3.4:9", Carrier: "quic", Addr: "1.2.3.4:9", Host: "1.2.3.4:9", SNI: "b.com", Path: "/ws", ALPN: []string{"h3"}, ALPNDefaulted: true, Dirs: DirBidi, Count: 1}},
		{"quic://a.com?alpn=h3", Candidate{URL: "quic://a.com:8025", Carrier: "quic", Addr: "a.com:8025", Host: "a.com", SNI: "a.com", Path: "/ws", ALPN: []string{"h3"}, Dirs: DirBidi, Count: 1}},
		{"ws://a.com/p", Candidate{URL: "http://a.com/p", HTTP: []string{HTTP11}, Carrier: "ws", Addr: "a.com:80", Host: "a.com", SNI: "a.com", Path: "/p", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"wss://a.com:8443/api/v2/ws", Candidate{URL: "https://a.com:8443/api/v2/ws", HTTP: []string{HTTP2, HTTP11}, Carrier: "wss", Addr: "a.com:8443", Host: "a.com:8443", SNI: "a.com", Path: "/api/v2/ws", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"http://a.com", Candidate{URL: "http://a.com", HTTP: []string{HTTP11}, Carrier: "http", Addr: "a.com:80", Host: "a.com", SNI: "a.com", Path: "/ws", Kinds: httpKinds, Dirs: DirBidi, Count: 1}},
		{"https://a.com/", Candidate{URL: "https://a.com", HTTP: []string{HTTP2, HTTP11}, Carrier: "https", Addr: "a.com:443", Host: "a.com", SNI: "a.com", Path: "/", Kinds: httpKinds, Dirs: DirBidi, Count: 1}},
		{"https://a.com/x%7Cy%3Fz?kinds=websocket", Candidate{URL: "https://a.com/x%7Cy%3Fz", HTTP: []string{HTTP2, HTTP11}, Carrier: "https", Addr: "a.com:443", Host: "a.com", SNI: "a.com", Path: "/x%7Cy%3Fz", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"WSS://a.com/ws?tier=1&weight=3&count=3&standby&dirs=up,down,bidi", Candidate{URL: "https://a.com/ws", HTTP: []string{HTTP2, HTTP11}, Carrier: "wss", Addr: "a.com:443", Host: "a.com", SNI: "a.com", Path: "/ws", Kinds: ws, Tier: 1, Weight: 3, Count: 3, Standby: true, Dirs: DirBidi | DirUp | DirDown}},
		{"wss://1.2.3.4:443/ws?host=b.com", Candidate{URL: "https://1.2.3.4/ws", HTTP: []string{HTTP2, HTTP11}, Carrier: "wss", Addr: "1.2.3.4:443", Host: "b.com:443", SNI: "b.com", Path: "/ws", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"wss://1.2.3.4/ws?host=b.com:8443&sni=c.com", Candidate{URL: "https://1.2.3.4/ws", HTTP: []string{HTTP2, HTTP11}, Carrier: "wss", Addr: "1.2.3.4:443", Host: "b.com:8443", SNI: "c.com", Path: "/ws", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"tcp://[::1]:8024?standby=true&tier=-2", Candidate{URL: "tcp://[::1]:8024", Carrier: "tcp", Addr: "[::1]:8024", Host: "[::1]:8024", Path: "/ws", Tier: -2, Standby: true, Dirs: DirBidi, Count: 1}},
		{"https://[::1]:443", Candidate{URL: "https://[::1]", HTTP: []string{HTTP2, HTTP11}, Carrier: "https", Addr: "[::1]:443", Host: "[::1]:443", Path: "/ws", Kinds: httpKinds, Dirs: DirBidi, Count: 1}},
		{"tls://[::1]", Candidate{URL: "tls://[::1]:8025", Carrier: "tls", Addr: "[::1]:8025", Host: "[::1]", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"tcp://a.com:1/", Candidate{URL: "tcp://a.com:1", Carrier: "tcp", Addr: "a.com:1", Host: "a.com:1", SNI: "a.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"https://a.com?kinds=websocket,streaming", Candidate{URL: "https://a.com", HTTP: []string{HTTP2, HTTP11}, Carrier: "https", Addr: "a.com:443", Host: "a.com", SNI: "a.com", Path: "/ws", Kinds: []string{KindWebSocket, KindStreaming}, Dirs: DirBidi, Count: 1}},
	}
	for _, c := range cases {
		got, err := Parse(c.in, "kcp")
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		c.want.Raw = c.in
		if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

func TestParseLegacy(t *testing.T) {
	cases := []struct {
		in, tp string
		want   Candidate
	}{
		{"1.2.3.4:8024", "tcp", Candidate{URL: "tcp://1.2.3.4:8024", Carrier: "tcp", Addr: "1.2.3.4:8024", Host: "1.2.3.4:8024", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"a.com", "TLS", Candidate{URL: "tls://a.com:8025", Carrier: "tls", Addr: "a.com:8025", Host: "a.com", SNI: "a.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"1.2.3.4:443@b.com:443/path", "wss", Candidate{URL: "https://1.2.3.4/path", HTTP: []string{HTTP2, HTTP11}, Carrier: "wss", Addr: "1.2.3.4:443", Host: "b.com:443", SNI: "b.com", Path: "/path", Kinds: ws, Dirs: DirBidi, Count: 1}},
		{"a.com@b.com/h3", "quic", Candidate{URL: "quic://a.com:8025", Carrier: "quic", Addr: "a.com:8025", Host: "b.com", SNI: "b.com", Path: "/h3", ALPN: []string{"h3"}, Dirs: DirBidi, Count: 1}},
		{"a.com", "quic", Candidate{URL: "quic://a.com:8025", Carrier: "quic", Addr: "a.com:8025", Host: "a.com", SNI: "a.com", Path: "/ws", ALPN: []string{"h3"}, ALPNDefaulted: true, Dirs: DirBidi, Count: 1}},
		{"[::1]:8024", "tcp", Candidate{URL: "tcp://[::1]:8024", Carrier: "tcp", Addr: "[::1]:8024", Host: "[::1]:8024", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"[::1]", "tcp", Candidate{URL: "tcp://[::1]:8024", Carrier: "tcp", Addr: "[::1]:8024", Host: "[::1]", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"a.com:1", "", Candidate{URL: "tcp://a.com:1", Carrier: "tcp", Addr: "a.com:1", Host: "a.com:1", SNI: "a.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
		{"a.com:1", "odd", Candidate{URL: "odd://a.com:1", Carrier: "odd", Addr: "a.com:1", Host: "a.com:1", SNI: "a.com", Path: "/ws", Dirs: DirBidi, Count: 1}},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.tp)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		c.want.Raw = c.in
		if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

// The URL form and the earlier form name the same dial address, Host and SNI.
func TestURLMatchesLegacy(t *testing.T) {
	for legacy, u := range map[string]string{
		"1.2.3.4:443@b.com:443/ws": "wss://1.2.3.4:443/ws?host=b.com:443",
		"1.2.3.4:8025@b.com":       "tls://1.2.3.4:8025?host=b.com",
	} {
		tp := strings.SplitN(u, ":", 2)[0]
		l, err1 := Parse(legacy, tp)
		n, err2 := Parse(u, "")
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if l[0].Addr != n[0].Addr || l[0].SNI != n[0].SNI || l[0].Path != n[0].Path || l[0].URL != n[0].URL {
			t.Errorf("%s vs %s:\n%+v\n%+v", legacy, u, l[0], n[0])
		}
	}
}

func TestParseMixed(t *testing.T) {
	got, err := Parse(" tls://a.com:8025 | b.com:8024@c.com/ws || wss://[::1]:443/ws?tier=1 |", "kcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates", len(got))
	}
	for i, want := range []string{"tls", "kcp", "wss"} {
		if got[i].Carrier != want || got[i].Index != i {
			t.Errorf("candidate %d: %+v", i, got[i])
		}
	}
	if got[1].Addr != "b.com:8024" || got[1].SNI != "c.com" || got[2].Addr != "[::1]:443" || got[2].Tier != 1 {
		t.Errorf("%+v", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		"", " | ", "ftp://a.com", "tcp://", "tcp://u:p@a.com", "tcp://a.com#x",
		"tcp://a.com:0", "tcp://a.com:99999", "tcp://a.com:x", "tcp://a.com/path", "quic://a.com/h3",
		"tcp://a.com?tier=x", "tcp://a.com?standby=maybe", "tcp://a.com?count=0", "tcp://a.com?weight=-1",
		"tcp://a.com?count=-1", "tcp://a.com?foo=1", "quic://a.com?alpn=", "tcp://a.com?alpn=x",
		"a.com|tcp://", "tcp://a.com?%zz", "tcp://a.com?dirs=sideways", "tcp://a.com?tier=1&tier=2",
		"tcp://a.com?dirs=up", "tcp://a.com?dirs=up|tls://b.com?dirs=down", "tcp://a.com?kinds=websocket",
		"https://a.com?host=", "tcp://a.com?Tier=1",
	} {
		if _, err := Parse(in, "tcp"); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestOrder(t *testing.T) {
	cands, err := Parse("tcp://s?standby=1|tcp://a?tier=2|tcp://b|tcp://c?tier=1|tcp://d|tcp://t?standby=1&tier=-5", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	names := func(cs []Candidate) string {
		var b strings.Builder
		for _, c := range cs {
			b.WriteString(c.Addr[:1])
		}
		return b.String()
	}
	if got := names(Order(cands, "")); got != "bdcats" {
		t.Errorf("order %s, want bdcats", got)
	}
	if got := names(Order(cands, "tcp://s?standby=1")); got != "sbdcat" {
		t.Errorf("order after s %s, want sbdcat", got)
	}
	if got := names(Order(cands, "tcp://a?tier=2")); got != "abdcts" {
		t.Errorf("order after a %s, want abdcts", got)
	}
	if got := names(cands); got != "sabcdt" {
		t.Errorf("Order changed its input: %s", got)
	}
}

func TestDirsAndMultiMembers(t *testing.T) {
	cands, err := Parse("tls://a.com:8025?tier=0&count=5|wss://b.com/ws?tier=0&dirs=up&count=3|tcp://c.com:8024?tier=1&dirs=DOWN&count=5|tcp://d.com?tier=2|tcp://e.com?standby=1&tier=9", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if !cands[0].Starts() || cands[1].Dirs != DirUp || cands[2].Dirs != DirDown || cands[1].Starts() || cands[2].Count != 5 {
		t.Fatalf("%+v", cands)
	}
	var got []string
	for _, c := range MultiMembers(cands) {
		got = append(got, c.Addr[:1])
	}
	if strings.Join(got, "") != "abce" {
		t.Errorf("multi members %v, want a b c e", got)
	}
}

func TestParsePipes(t *testing.T) {
	for in, want := range map[string]string{"": PipesSingle, "single": PipesSingle, " Multi ": PipesMulti} {
		if got, err := ParsePipes(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := ParsePipes("double"); err == nil {
		t.Error("double: no error")
	}
}

func TestHTTPAndALPNParameters(t *testing.T) {
	for _, tc := range []struct {
		in   string
		http []string
		alpn []string
		bad  bool
	}{
		{in: "https://a.com/ws", http: []string{HTTP2, HTTP11}},
		{in: "wss://a.com/ws", http: []string{HTTP2, HTTP11}},
		{in: "ws://a.com/ws", http: []string{HTTP11}},
		{in: "https://a.com/x?http=h3,h2,http/1.1", http: []string{HTTP3, HTTP2, HTTP11}},
		{in: "tls://a.com?alpn=x,y", alpn: []string{"x", "y"}},
		{in: "quic://a.com", alpn: []string{DefaultQUICALPN}},
		{in: "wss://a.com/ws?http=h3", bad: true},
		{in: "https://a.com?alpn=h2", bad: true},
		{in: "tcp://a.com?http=h2", bad: true},
		{in: "https://a.com?http=h4", bad: true},
		{in: "https://a.com?http=h2,h2", bad: true},
	} {
		cs, err := Parse(tc.in, "tcp")
		if tc.bad {
			if err == nil {
				t.Errorf("%s: accepted", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if !slices.Equal(cs[0].HTTP, tc.http) || !slices.Equal(cs[0].ALPN, tc.alpn) {
			t.Errorf("%s: http %v alpn %v", tc.in, cs[0].HTTP, cs[0].ALPN)
		}
	}
}

func TestConnectionLists(t *testing.T) {
	for _, tc := range []struct {
		server string
		sep    string
		n      int
	}{
		{"a.com:8024,b.com:8024", SepLegacy, 2},
		{"a.com:8024，b.com:8024", SepLegacy, 2},
		{"tls://a.com:8025|https://a.com/ws?http=h2,http/1.1;tcp://b.com:8024", SepURL, 2},
		{"tls://a.com:8025；a.com:8024", SepURL, 2},
		{"wss://a.com/ws?dirs=up,down|tcp://a.com:8024", SepURL, 1},
	} {
		if sep := ListSep(tc.server); sep != tc.sep {
			t.Fatalf("%s: sep %q", tc.server, sep)
		}
		if got := SplitList(tc.server, tc.sep); len(got) != tc.n {
			t.Fatalf("%s: %d connections %v", tc.server, len(got), got)
		}
		if err := CheckList(tc.server, nil); err != nil {
			t.Fatalf("%s: %v", tc.server, err)
		}
	}
	err := CheckList("tls://a.com:8025,tls://b.com:8025", nil)
	if err == nil || !strings.Contains(err.Error(), `";"`) {
		t.Fatalf("comma-separated URL connections: %v", err)
	}
}

func TestUsesType(t *testing.T) {
	if !UsesType("a.com:8024,b.com:8024") || !UsesType("tls://a.com:8025|b.com:8024") || UsesType("tls://a.com:8025|wss://a.com/ws;tcp://b.com:8024") {
		t.Fatal("UsesType")
	}
}

// The start commands of the web interface write -server as a URL of host,
// port and, for WebSocket, the bridge path; IPv6 hosts are bracketed.
func TestWebStartCommandServers(t *testing.T) {
	for _, c := range []struct{ in, carrier, addr, path, url string }{
		{"tcp://nps.example.com:8024", "tcp", "nps.example.com:8024", "", "tcp://nps.example.com:8024"},
		{"tcp://[2001:db8::1]:8024", "tcp", "[2001:db8::1]:8024", "", "tcp://[2001:db8::1]:8024"},
		{"tls://[2001:db8::1]:8025", "tls", "[2001:db8::1]:8025", "", "tls://[2001:db8::1]:8025"},
		{"quic://[::1]:8025?alpn=nps", "quic", "[::1]:8025", "", "quic://[::1]:8025"},
		{"ws://nps.example.com:8026/ws", "ws", "nps.example.com:8026", "/ws", "http://nps.example.com:8026/ws"},
		{"ws://[2001:db8::1]:8026/a/b", "ws", "[2001:db8::1]:8026", "/a/b", "http://[2001:db8::1]:8026/a/b"},
		{"wss://[2001:db8::1]:8027/a/b", "wss", "[2001:db8::1]:8027", "/a/b", "https://[2001:db8::1]:8027/a/b"},
	} {
		got, err := Parse(c.in, "tcp")
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %v, %d candidates", c.in, err, len(got))
		}
		g := got[0]
		wantPath := c.path
		if c.carrier != "ws" && c.carrier != "wss" {
			wantPath = "/ws"
		}
		if g.Carrier != c.carrier || g.Addr != c.addr || g.Path != wantPath || g.URL != c.url {
			t.Errorf("%s: carrier %s addr %s path %s url %s", c.in, g.Carrier, g.Addr, g.Path, g.URL)
		}
	}
}

func TestAttempts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"http://a.com/x", []string{"#http/1.1/websocket", "#http/1.1/streaming", "#http/1.1/split"}},
		{"https://a.com/x?http=http/1.1", []string{"#http/1.1/websocket", "#http/1.1/streaming", "#http/1.1/split"}},
		{"https://a.com/x", []string{"#h2/streaming", "#h2/split", "#http/1.1/websocket", "#http/1.1/streaming", "#http/1.1/split"}},
		{"https://a.com/x?kinds=split&http=h2", []string{"#h2/split"}},
		{"http://a.com/x?http=h3,http/1.1&kinds=streaming", []string{"#http/1.1/streaming"}},
		{"wss://a.com/x", []string{""}},
		{"https://a.com/x?http=h3&kinds=http3", []string{"#h3/http3"}},
		{"https://a.com/x?http=h3,h2", []string{"#h3/http3", "#h2/streaming", "#h2/split"}},
	} {
		c, err := Parse(tc.in, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		var got []string
		for _, a := range Attempts(c) {
			got = append(got, a.Attempt)
			if a.Key() != c[0].Raw+a.Attempt {
				t.Fatalf("%s: key %q", tc.in, a.Key())
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.in, got, tc.want)
		}
	}
	if a := Attempts([]Candidate{mustParse(t, "https://a.com/x?kinds=websocket&http=http/1.1")})[0]; a.Carrier != "wss" {
		t.Fatalf("websocket attempt carrier %q", a.Carrier)
	}
	for _, bad := range []string{"http://a.com?http=h2", "https://a.com?http=h2&kinds=http3", "http://a.com?dirs=up", "https://a.com?kinds=websocket,websocket", "https://a.com?kinds=x"} {
		if _, err := Parse(bad, ""); err == nil {
			t.Fatalf("%s: no error", bad)
		}
	}
}

func mustParse(t *testing.T, s string) Candidate {
	t.Helper()
	c, err := Parse(s, "")
	if err != nil {
		t.Fatal(err)
	}
	return c[0]
}

func TestHTTPLinkRoundTrip(t *testing.T) {
	for _, raw := range []string{"http://a.com:80/x", "https://a.com:443/x?kinds=streaming"} {
		link, err := FormatLink("k", raw, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		l, err := ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		a, b := mustParse(t, raw), mustParse(t, l.Server)
		if a.Carrier != b.Carrier || a.Path != b.Path || !slices.Equal(a.Kinds, b.Kinds) {
			t.Fatalf("%s: %+v != %+v", link, b, a)
		}
	}
}
