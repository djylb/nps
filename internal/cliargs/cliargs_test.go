package cliargs

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestPrepare(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("server", "", "")
	fs.String("conf_path", "", "")
	fs.BoolP("version", "v", false, "")
	fs.BoolP("debug", "d", false, "")
	commands := []string{"install", "update"}
	for _, tc := range []struct {
		name         string
		args         []string
		out, ignored []string
	}{
		{"known long, single dash", []string{"-server=a:1"}, []string{"--server=a:1"}, nil},
		{"known long, double dash", []string{"--server", "a:1"}, []string{"--server", "a:1"}, nil},
		{"normalized name", []string{"-conf-path=x"}, []string{"--conf-path=x"}, nil},
		{"shorthands", []string{"-v", "-vd"}, []string{"-v", "-vd"}, nil},
		{"unknown with =", []string{"-old=true", "-server=a:1"}, []string{"--server=a:1"}, []string{"-old=true"}},
		{"unknown bool", []string{"-old", "-server", "a:1"}, []string{"--server", "a:1"}, []string{"-old"}},
		{"unknown with value", []string{"-old", "0", "install"}, []string{"install"}, []string{"-old 0"}},
		{"unknown before a command", []string{"--old", "install"}, []string{"install"}, []string{"--old"}},
		{"unknown shorthand", []string{"-x"}, nil, []string{"-x"}},
		{"after --", []string{"--", "-unknown"}, []string{"--", "-unknown"}, nil},
		{"positional", []string{"update", "-"}, []string{"update", "-"}, nil},
	} {
		out, warnings := Prepare(fs, tc.args, nil, commands...)
		if !slices.Equal(out, tc.out) && !(len(out) == 0 && len(tc.out) == 0) {
			t.Errorf("%s: out = %q, want %q", tc.name, out, tc.out)
		}
		var want []string
		for _, a := range tc.ignored {
			want = append(want, "unknown argument \""+a+"\" ignored")
		}
		if !slices.Equal(warnings, want) {
			t.Errorf("%s: warnings = %q, want %q", tc.name, warnings, want)
		}
	}
}

// npcFlags returns flags as npc defines them, for the rules of NPC.
func npcFlags() (fs *pflag.FlagSet, server, vkey, connType *string, debug *bool) {
	fs = pflag.NewFlagSet("npc", pflag.ContinueOnError)
	fs.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(normalize(name))
	})
	server = fs.String("server", "", "")
	vkey = fs.String("vkey", "", "")
	connType = fs.StringP("type", "t", "tcp", "")
	debug = fs.Bool("debug", true, "")
	return
}

// TestNPCRules covers each rule of NPC.
func TestNPCRules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		connType string
		rest     []string // positional arguments left
		warning  string   // part of the one warning, "" for none
	}{
		{"tls_enable", []string{"-tls_enable"}, "tls", nil, "-type=tls"},
		{"--tls_enable", []string{"--tls_enable"}, "tls", nil, "-type=tls"},
		{"tls-enable", []string{"-tls-enable"}, "tls", nil, "-type=tls"},
		{"tls_enable=true", []string{"-tls_enable=true"}, "tls", nil, "-type=tls"},
		{"tls_enable=1", []string{"--tls_enable=1"}, "tls", nil, "-type=tls"},
		{"tls_enable=false", []string{"-tls_enable=false"}, "tcp", nil, "no effect"},
		{"tls_enable=false keeps type", []string{"-type=kcp", "-tls_enable=false"}, "kcp", nil, "no effect"},
		{"tls_enable=bad", []string{"-tls_enable=yes please"}, "tcp", nil, "not a boolean"},
		{"tls_enable takes no value", []string{"-tls_enable", "install"}, "tls", []string{"install"}, "-type=tls"},
		{"tls_enable over a later -type", []string{"-tls_enable", "-type=kcp"}, "tls", nil, "-type=tls"},
		{"tls_enable over an earlier -type", []string{"-type=quic,tcp", "-tls_enable"}, "tls", nil, "-type=tls"},
		{"tls_enable over -t", []string{"-t", "ws", "-tls_enable"}, "tls", nil, "-type=tls"},
		{"proto_version", []string{"-proto_version=0"}, "tcp", nil, "latest protocol"},
		{"proto_version value", []string{"-proto_version", "2", "install"}, "tcp", []string{"install"}, "latest protocol"},
		{"proto_version alone", []string{"--proto_version"}, "tcp", nil, "latest protocol"},
		{"stun_addr", []string{"-stun_addr", "stun.example:3478"}, "tcp", nil, "p2p_stun_servers"},
		{"p2p_type", []string{"-p2p_type=kcp"}, "tcp", nil, "no longer has an effect"},
		{"log_daily", []string{"-log_daily", "true"}, "tcp", nil, "no longer has an effect"},
		{"unknown", []string{"-gone", "x"}, "tcp", nil, "unknown argument \"-gone x\" ignored"},
	} {
		fs, _, _, connType, _ := npcFlags()
		out, warnings := Prepare(fs, tc.args, NPC, "install")
		if err := fs.Parse(out); err != nil {
			t.Errorf("%s: parse %q: %v", tc.name, out, err)
			continue
		}
		if *connType != tc.connType {
			t.Errorf("%s: type %q, want %q", tc.name, *connType, tc.connType)
		}
		if !slices.Equal(fs.Args(), tc.rest) && !(len(fs.Args()) == 0 && len(tc.rest) == 0) {
			t.Errorf("%s: args %q, want %q", tc.name, fs.Args(), tc.rest)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], tc.warning) {
			t.Errorf("%s: warnings %q, want one with %q", tc.name, warnings, tc.warning)
		}
	}
}

// TestNPCOldServiceArgs parses the arguments services installed by earlier
// npc versions pass.
func TestNPCOldServiceArgs(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		args                   []string
		server, vkey, connType string
		debug                  bool
		rest                   []string
		warnings               int
	}{
		{"0.34 tls", []string{"-server=a:1", "-vkey=k", "-tls_enable", "-debug=false"}, "a:1", "k", "tls", false, nil, 1},
		{"0.34 tls, service", []string{"-server=a:1", "-vkey=k", "-tls_enable=true", "-debug=false", "service"}, "a:1", "k", "tls", false, []string{"service"}, 1},
		{"0.26 single dash", []string{"-server=a:1", "-vkey=k", "-type=tcp", "-log_daily=false", "-stun_addr=s:3478"}, "a:1", "k", "tcp", true, nil, 2},
		{"0.26 tls", []string{"-server", "a:1", "-vkey", "k", "-tls_enable=true", "-log_daily", "true"}, "a:1", "k", "tls", true, nil, 2},
		{"0.33 proto", []string{"-server=a:1,b:2", "-vkey=k", "-type=tcp,kcp", "-proto_version=1", "-p2p_type=quic"}, "a:1,b:2", "k", "tcp,kcp", true, nil, 2},
		{"0.34 multi tls", []string{"-server=a:1,b:2", "-vkey=k,l", "-type=tcp,kcp", "-tls_enable"}, "a:1,b:2", "k,l", "tls", true, nil, 1},
	} {
		fs, server, vkey, connType, debug := npcFlags()
		out, warnings := Prepare(fs, tc.args, NPC, "install", "service")
		if err := fs.Parse(out); err != nil {
			t.Errorf("%s: parse %q: %v", tc.name, out, err)
			continue
		}
		if *server != tc.server || *vkey != tc.vkey || *connType != tc.connType || *debug != tc.debug {
			t.Errorf("%s: got server %q vkey %q type %q debug %v", tc.name, *server, *vkey, *connType, *debug)
		}
		if !slices.Equal(fs.Args(), tc.rest) && !(len(fs.Args()) == 0 && len(tc.rest) == 0) {
			t.Errorf("%s: args %q, want %q", tc.name, fs.Args(), tc.rest)
		}
		if len(warnings) != tc.warnings {
			t.Errorf("%s: warnings %q", tc.name, warnings)
		}
	}
}

func TestPrepareKeepsHelp(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	for _, a := range []string{"-h", "--help", "-help"} {
		out, warnings := Prepare(fs, []string{a}, nil)
		if len(out) != 1 || out[0] != a || len(warnings) != 0 {
			t.Fatalf("%s -> %v %v", a, out, warnings)
		}
	}
}
