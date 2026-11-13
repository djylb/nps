package bridge

import "testing"

func TestPipeSummary(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"tls"}, "tls"},
		{[]string{"quic", "tls", "tls"}, "tls×2 + quic"},
		{[]string{"ws", "tls", "quic"}, "quic + tls + ws"},
	} {
		if got := pipeSummary(tc.in); got != tc.want {
			t.Errorf("pipeSummary(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
