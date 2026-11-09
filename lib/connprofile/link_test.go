package connprofile

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	const pin = "00112233445566778899aabbccddeeff"
	tests := []struct {
		in      string
		vkey    string
		server  string
		name    string
		common  map[string]string
		carrier string
	}{
		{in: "npc://k@a.com:8024", vkey: "k", server: "tcp://a.com:8024", carrier: "tcp"},
		{in: "NPC://k@a.com:8025?type=TLS&alpn=h2,http/1.1&sni=b.com&host=c.com", vkey: "k",
			server: "tls://a.com:8025?alpn=h2%2Chttp%2F1.1&host=c.com&sni=b.com", carrier: "tls"},
		{in: "npc://k@a.com:443/ws/x?type=wss&http=h2&subprotocol=zmux", vkey: "k",
			server: "wss://a.com:443/ws/x?http=h2&subprotocol=zmux", carrier: "wss"},
		{in: "npc://k@a.com:80/p?type=ws", vkey: "k", server: "ws://a.com:80/p", carrier: "ws"},
		{in: "npc://k@a.com:8025?type=quic&alpn=h3", vkey: "k", server: "quic://a.com:8025?alpn=h3", carrier: "quic"},
		{in: "npc://k@[2001:db8::1]:8024?type=tls", vkey: "k", server: "tls://[2001:db8::1]:8024", carrier: "tls"},
		{in: "npc://p%40ss%3A%2F%3B%2C%20w@a.com:1", vkey: "p@ss:/;, w", server: "tcp://a.com:1", carrier: "tcp"},
		{in: "npc://k@a.com:1?type=tls&tier=1&weight=3&standby&count=2&alt=tcp://b.com:2&alt=" + url.QueryEscape("wss://c.com:443@front.com/ws?tier=2") + "#home", vkey: "k",
			server: "tls://a.com:1?count=2&standby=&tier=1&weight=3|tcp://b.com:2|wss://c.com:443/ws?host=front.com&tier=2", name: "home", carrier: "tls"},
		{in: "npc://k@a.com:1?name=nas&pin=" + pin + "&proxy=socks5://p:1&pipes=multi&session_max_lanes=4&source=10.0.0.2", vkey: "k", server: "tcp://a.com:1", name: "nas", carrier: "tcp",
			common: map[string]string{"new_protocol_pin": pin, "proxy_url": "socks5://p:1", "pipes": "multi", "session_max_lanes": "4", "local_ip": "10.0.0.2"}},
		{in: "npc://k@a.com:1?future=1&exp=5", vkey: "k", server: "tcp://a.com:1", carrier: "tcp"},
	}
	for _, tt := range tests {
		l, err := ParseLink(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if l.VKey != tt.vkey || l.Server != tt.server || l.Name != tt.name {
			t.Fatalf("%s: got %+v", tt.in, l)
		}
		if len(tt.common) > 0 && !reflect.DeepEqual(l.Common, tt.common) {
			t.Fatalf("%s: common %v", tt.in, l.Common)
		}
		if len(tt.common) == 0 && len(l.Common) != 0 {
			t.Fatalf("%s: stray settings %+v", tt.in, l)
		}
		cands, err := Parse(l.Server, "")
		if err != nil || cands[0].Carrier != tt.carrier {
			t.Fatalf("%s: %v %+v", tt.in, err, cands)
		}
	}
}

func TestParseLinkCandidates(t *testing.T) {
	l, err := ParseLink("npc://k@a.com:443/p?type=wss&subprotocol=zmux&http=h2&host=f.com&alt=tls://b.com:1@g.com?alpn=h2")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(l.Server, "")
	if err != nil || len(c) != 2 {
		t.Fatalf("%v %v", c, err)
	}
	if c[0].Subprotocol != "zmux" || c[0].Host != "f.com:443" || c[0].Path != "/p" || len(c[0].HTTP) != 1 || c[0].HTTP[0] != HTTP2 {
		t.Fatalf("%+v", c[0])
	}
	if c[1].Carrier != "tls" || c[1].Addr != "b.com:1" || c[1].Host != "g.com:1" || c[1].ALPN[0] != "h2" {
		t.Fatalf("%+v", c[1])
	}
}

func TestParseLinkRejects(t *testing.T) {
	for name, in := range map[string]string{
		"scheme":        "nps://k@a:1",
		"http":          "http://k@a:1",
		"no vkey":       "npc://a:1",
		"empty vkey":    "npc://@a:1",
		"client number": "npc://7:k@a:1",
		"no host":       "npc://k@:1",
		"type auto":     "npc://k@a:1?type=auto",
		"type kcp":      "npc://k@a:1?type=kcp",
		"type unknown":  "npc://k@a:1?type=x",
		"twice":         "npc://k@a:1?type=tls&type=tcp",
		"alpn on tcp":   "npc://k@a:1?alpn=h2",
		"http on tls":   "npc://k@a:1?type=tls&http=h2",
		"subproto tls":  "npc://k@a:1?type=tls&subprotocol=x",
		"path on tcp":   "npc://k@a:1/x",
		"alt no scheme": "npc://k@a:1?alt=b:2",
		"alt kcp":       "npc://k@a:1?alt=kcp://b:2",
		"bad pin":       "npc://k@a:1?pin=abcd",
		"access":        "npc://k@a:1?access=a",
		"password":      "npc://k@a:1?password=a",
		"target":        "npc://k@a:1?target=t:22",
		"local type":    "npc://k@a:1?local_type=tcp",
		"local port":    "npc://k@a:1?local_port=1",
		"local ip":      "npc://k@a:1?local_ip=1.1.1.1",
		"conn mode":     "npc://k@a:1?conn_mode=auto",
		"local proxy":   "npc://k@a:1?local_proxy=true",
		"bad port":      "npc://k@a:99999",
		"too long":      "npc://k@a:1?name=" + strings.Repeat("x", MaxLinkSize),
	} {
		l, err := ParseLink(in)
		if err == nil {
			t.Errorf("%s: accepted %+v", name, l)
			continue
		}
		if strings.Contains(err.Error(), "k@") && len(in) < 100 {
			t.Errorf("%s: the error shows the link: %v", name, err)
		}
	}
}

// A formatted link reads back as the same connection.
func TestFormatLinkRoundTrip(t *testing.T) {
	for _, tc := range [][3]string{
		{"tls://a.com:1?alpn=h2&count=2&tier=1|wss://b.com:443/x?http=h2&weight=3|tcp://c.com:2?standby=true", "tls", "p@ss w"},
		{"a.com:8024", "tcp", "k"},
		{"a.com:8025/h3x", "quic", "k"},
	} {
		link, err := FormatLink(tc[2], tc[0], tc[1], "home", map[string]string{"pipes": "multi", "local_ip": "10.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		l, err := ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if l.VKey != tc[2] || l.Name != "home" || l.Common["pipes"] != "multi" || l.Common["local_ip"] != "10.0.0.1" {
			t.Fatalf("%s: %+v", link, l)
		}
		want, _ := Parse(tc[0], tc[1])
		got, err := Parse(l.Server, "")
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s: %v %+v", link, err, got)
		}
		for i := range got {
			got[i].Raw, want[i].Raw, got[i].Path = "", "", want[i].Path
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
			}
		}
	}
}

// The alt values keep their own "?", "=" and ";", which url.ParseQuery
// would refuse.
func TestParseLinkAltParameters(t *testing.T) {
	for _, in := range []string{
		"npc://k@a.com:1?pipes=multi&count=2&alt=quic://b:443?tier=1;weight=2&alt=tls://c:8025?alpn=h2,nps;count=3;standby=1",
		"npc://k@a.com:1?pipes=multi&count=2&alt=" + url.QueryEscape("quic://b:443?tier=1&weight=2") + "&alt=" + url.QueryEscape("tls://c:8025?alpn=h2,nps&count=3&standby=1"),
	} {
		l, err := ParseLink(in)
		if err != nil {
			t.Fatal(err)
		}
		if l.Common["pipes"] != "multi" {
			t.Fatalf("%v", l.Common)
		}
		c, err := Parse(l.Server, "")
		if err != nil || len(c) != 3 {
			t.Fatalf("%v %v", c, err)
		}
		if c[0].Count != 2 || c[1].Carrier != "quic" || c[1].Tier != 1 || c[1].Weight != 2 ||
			c[2].Carrier != "tls" || c[2].Count != 3 || !c[2].Standby || len(c[2].ALPN) != 2 || c[2].ALPN[1] != "nps" {
			t.Fatalf("%+v", c)
		}
	}
	if _, err := ParseLink("npc://k@a:1?alt=tcp://b:1?tier=x"); err == nil {
		t.Error("a bad alt parameter was accepted")
	}
}

// The example of the launch documentation.
func TestParseLinkDocumentedExample(t *testing.T) {
	l, err := ParseLink("npc://VKEY@nps.example.com:8025?type=tls&alpn=h2&count=2&pipes=multi&alt=wss://nps.example.com:443/ws?tier=1;weight=2;http=h2&alt=tcp://nps.example.com:8024?standby=1&name=office")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(l.Server, "")
	if err != nil || len(c) != 3 || c[1].Weight != 2 || !c[2].Standby || l.Name != "office" {
		t.Fatalf("%+v %v", c, err)
	}
}
