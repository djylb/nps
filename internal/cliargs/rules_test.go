package cliargs

import (
	"slices"
	"testing"
)

func TestNPCLocal(t *testing.T) {
	for _, tc := range []struct {
		in, want []string
		warn     bool
	}{
		{[]string{"-password=p", "-local_type=mixed"}, []string{"-password=p", "--local_type=mixed"}, false},
		{[]string{"-password=p"}, []string{"-password=p"}, false},
		{[]string{"-local_type", "p2p", "-target_type", "udp"}, []string{"--local_type=udp", "--conn_mode=auto"}, true},
		{[]string{"-local_type=secret", "-target_type=tcp"}, []string{"--local_type=tcp", "--conn_mode=relay"}, true},
		{[]string{"-local_type=p2ps", "-fallback_secret=false"}, []string{"--local_type=mixed", "--conn_mode=direct"}, true},
		{[]string{"-local_type=p2pt", "-disable_p2p"}, []string{"--local_type=transparent", "--conn_mode=relay"}, true},
		{[]string{"-target_type=tcp", "-conn_mode=direct"}, []string{"-conn_mode=direct", "--local_type=tcp"}, true},
		{[]string{"-fallback_secret", "start"}, []string{"start", "--local_type=tcp_udp", "--conn_mode=auto"}, true},
	} {
		got, warnings := NPCLocal(tc.in)
		if !slices.Equal(got, tc.want) || (len(warnings) > 0) != tc.warn {
			t.Errorf("%q: got %q %q, want %q", tc.in, got, warnings, tc.want)
		}
	}
}
