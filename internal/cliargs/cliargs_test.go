package cliargs

import (
	"slices"
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
		{"unknown with =", []string{"-tls_enable=true", "-server=a:1"}, []string{"--server=a:1"}, []string{"-tls_enable=true"}},
		{"unknown bool", []string{"-tls_enable", "-server", "a:1"}, []string{"--server", "a:1"}, []string{"-tls_enable"}},
		{"unknown with value", []string{"-proto_version", "0", "install"}, []string{"install"}, []string{"-proto_version 0"}},
		{"unknown before a command", []string{"--old", "install"}, []string{"install"}, []string{"--old"}},
		{"unknown shorthand", []string{"-x"}, nil, []string{"-x"}},
		{"after --", []string{"--", "-unknown"}, []string{"--", "-unknown"}, nil},
		{"positional", []string{"update", "-"}, []string{"update", "-"}, nil},
	} {
		out, ignored := Prepare(fs, tc.args, commands...)
		if !slices.Equal(out, tc.out) && !(len(out) == 0 && len(tc.out) == 0) {
			t.Errorf("%s: out = %q, want %q", tc.name, out, tc.out)
		}
		if !slices.Equal(ignored, tc.ignored) {
			t.Errorf("%s: ignored = %q, want %q", tc.name, ignored, tc.ignored)
		}
	}
}
