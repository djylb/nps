package config

import "testing"

func TestLegacyTLSEnable(t *testing.T) {
	for _, tc := range []struct {
		name, common, want string
	}{
		{"tls_enable=true", "tls_enable=true", "tls"},
		{"before conn_type", "tls_enable=true\nconn_type=kcp", "tls"},
		{"after conn_type", "conn_type=quic\ntls_enable=true", "tls"},
		{"tls_enable=false", "conn_type=kcp\ntls_enable=false", "kcp"},
		{"not set", "conn_type=ws", "ws"},
		{"default", "", "tcp"},
	} {
		c, err := Parse("[common]\nserver_addr=1.2.3.4:8025\nvkey=k\n" + tc.common + "\n")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.CommonConfig.Tp != tc.want {
			t.Errorf("%s: conn type %q, want %q", tc.name, c.CommonConfig.Tp, tc.want)
		}
	}
}

func TestLegacySection(t *testing.T) {
	if legacySection("health_a", map[string]bool{"health_check_timeout": true}) == "" {
		t.Error("health section not reported")
	}
	if legacySection("web", map[string]bool{"host": true}) != "" {
		t.Error("ordinary section reported")
	}
}
