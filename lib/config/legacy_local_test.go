package config

import "testing"

func TestLegacyLocalSections(t *testing.T) {
	head := "[common]\nserver_addr=a:1\nvkey=k\n"
	for _, tc := range []struct{ name, body, typ, mode string }{
		{"old bare secret stays relayed", "[secret_a]\npassword=p\n", LocalTCPUDP, ConnRelay},
		{"new defaults", "[secret_a]\npassword=p\nlocal_type=tcp_udp\n", LocalTCPUDP, ConnAuto},
		{"new keys", "[secret_a]\npassword=p\nlocal_type=mixed\nconn_mode=direct\n", LocalMixed, ConnDirect},
		{"old secret", "[secret_a]\npassword=p\nlocal_type=secret\ntarget_type=udp\n", LocalUDP, ConnRelay},
		{"old secret target_type", "[secret_a]\npassword=p\ntarget_type=tcp\n", LocalTCP, ConnRelay},
		{"old p2p section", "[p2p_a]\npassword=p\n", LocalTCPUDP, ConnAuto},
		{"old p2p udp", "[p2p_a]\npassword=p\ntarget_type=udp\n", LocalUDP, ConnAuto},
		{"old p2ps no fallback", "[p2p_a]\npassword=p\nlocal_type=p2ps\nfallback_secret=false\n", LocalMixed, ConnDirect},
		{"old p2pt", "[p2p_a]\npassword=p\nlocal_type=p2pt\n", LocalTransparent, ConnAuto},
		{"old p2p in secret section", "[secret_a]\npassword=p\nlocal_type=p2p\n", LocalTCPUDP, ConnAuto},
		{"conn_mode wins", "[p2p_a]\npassword=p\nfallback_secret=false\nconn_mode=relay\n", LocalTCPUDP, ConnRelay},
	} {
		c, err := parseConfig(head + tc.body)
		if err != nil || len(c.LocalServer) != 1 {
			t.Fatalf("%s: %v %+v", tc.name, err, c)
		}
		if l := c.LocalServer[0]; l.Type != tc.typ || l.ConnMode != tc.mode || l.Password != "p" {
			t.Errorf("%s: got %s/%s, want %s/%s", tc.name, l.Type, l.ConnMode, tc.typ, tc.mode)
		}
	}
}

func TestLegacyP2PTask(t *testing.T) {
	c, err := parseConfig("[common]\nserver_addr=a:1\nvkey=k\n[t]\nmode=p2p\npassword=pw\ntarget_addr=127.0.0.1:22\n")
	if err != nil || len(c.Tasks) != 1 {
		t.Fatalf("%v %+v", err, c)
	}
	if c.Tasks[0].Mode != "secret" || c.Tasks[0].Password != "pw" {
		t.Fatalf("%+v", c.Tasks[0])
	}
}
