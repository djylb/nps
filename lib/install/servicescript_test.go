package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var awkwardArgs = []string{
	"service", "--server=tls://a.com:8025|wss://a.com/ws?tier=1&count=2", "two words", "it's", `say "hi"`,
	"a;b", "x&y", "$HOME", "${PATH}", "$(id)", "`id`", "100%", "%n", `back\slash`, "{{.}}", "{{{x}}}", "", "*", "-",
}

// printArgs writes a program that prints its arguments, each followed by
// NUL, into a directory whose name needs quoting itself.
func printArgs(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	dir := filepath.Join(t.TempDir(), "my dir's $x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "print args")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nfor a; do printf '%s\\0' \"$a\"; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func runSh(t *testing.T, script string) []string {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("sh: %v\n%s", err, script)
	}
	got := strings.Split(string(out), "\x00")
	return got[:len(got)-1]
}

// line returns the line of script starting with prefix (after indentation).
func line(t *testing.T, script, prefix string) string {
	t.Helper()
	for l := range strings.SplitSeq(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return strings.TrimSpace(l)
		}
	}
	t.Fatalf("no line %q in\n%s", prefix, script)
	return ""
}

func checkArgs(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: program got\n%q\nwant\n%q", name, got, want)
	}
}

func TestServiceScriptsQuoteForShell(t *testing.T) {
	exe := printArgs(t)
	for _, platform := range []string{"unix-systemv", "linux-procd", "linux-openrc", "linux-upstart", "linux-systemd"} {
		key, script := serviceScript(platform, exe, awkwardArgs)
		if key == "" {
			t.Fatalf("%s: no script", platform)
		}
		base := map[string]string{"unix-systemv": sysvScript, "linux-procd": procdScript, "linux-openrc": openrcScript,
			"linux-upstart": upstartScript, "linux-systemd": systemdScript}[platform]
		if strings.Count(script, "{{") != strings.Count(base, "{{") {
			t.Errorf("%s: the command line adds a template action", platform)
		}
		if strings.Contains(script, "@EXEC@") || strings.Contains(script, "@PATH@") || strings.Contains(script, "@ARGS@") {
			t.Errorf("%s: placeholder left", platform)
		}
	}
	want := append([]string{}, awkwardArgs...)

	_, sysv := serviceScript("unix-systemv", exe, awkwardArgs)
	if got := parseSysvCmd(sysv); got != exe {
		t.Errorf("parseSysvCmd = %q, want %q", got, exe)
	}
	fn := sysv[strings.Index(sysv, "run_cmd() {"):]
	fn = fn[:strings.Index(fn, "\n}\n")+3]
	checkArgs(t, "sysv", runSh(t, line(t, sysv, "cmd=")+"\n"+fn+"run_cmd & wait\n"), want)
	checkArgs(t, "sysv cmd", runSh(t, line(t, sysv, "cmd=")+"\n\"$cmd\" ok\n"), []string{"ok"})

	_, procd := serviceScript("linux-procd", exe, awkwardArgs)
	if got := parseSysvCmd(procd); got != exe {
		t.Errorf("parseSysvCmd(procd) = %q, want %q", got, exe)
	}
	if start, stop := rcOrder([]byte(procd)); start != "21" || stop != "89" {
		t.Errorf("procd order %s %s", start, stop)
	}
	stub := "procd_set_param() { for a; do printf '%s\\0' \"$a\"; done; }\n"
	checkArgs(t, "procd", runSh(t, stub+line(t, procd, "procd_set_param command")+"\n"), append([]string{"command", exe}, want...))

	_, openrc := serviceScript("linux-openrc", exe, awkwardArgs)
	// openrc-run passes command_args through eval
	checkArgs(t, "openrc", runSh(t, line(t, openrc, "command=")+"\n"+line(t, openrc, "command_args=")+
		"\neval \"set -- $command_args\"\n\"$command\" \"$@\"\n"), want)

	_, upstart := serviceScript("linux-upstart", exe, awkwardArgs)
	l := line(t, upstart, "exec {{SudoPrefix}}")
	l = strings.Replace(l, "{{SudoPrefix}}", "", 1)
	l = l[:strings.Index(l, "{{if LogOutput}}")]
	checkArgs(t, "upstart", runSh(t, l), append([]string{}, want...))
}

// systemdWords splits an ExecStart value as systemd does for the quoting
// systemdQuote uses: "..." words with \\, \", \x7b, and %% and $$.
func systemdWords(t *testing.T, s string) []string {
	t.Helper()
	var words []string
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimLeft(s, " ") {
		if s[0] != '"' {
			t.Fatalf("unquoted word at %q", s)
		}
		var b strings.Builder
		i := 1
		for ; i < len(s) && s[i] != '"'; i++ {
			switch {
			case s[i] == '\\' && strings.HasPrefix(s[i:], `\x7b`):
				b.WriteByte('{')
				i += 3
			case s[i] == '\\':
				i++
				b.WriteByte(s[i])
			case s[i] == '%' || s[i] == '$':
				if i+1 >= len(s) || s[i+1] != s[i] {
					t.Fatalf("unescaped %c in %q", s[i], s)
				}
				b.WriteByte(s[i])
				i++
			case s[i] == '{':
				t.Fatalf("raw { in %q", s)
			default:
				b.WriteByte(s[i])
			}
		}
		words = append(words, b.String())
		s = s[i+1:]
	}
	return words
}

func TestSystemdScriptQuotes(t *testing.T) {
	_, script := serviceScript("linux-systemd", "/usr/bin/npc", awkwardArgs)
	start := strings.TrimPrefix(line(t, script, "ExecStart="), "ExecStart=")
	checkArgs(t, "systemd", systemdWords(t, start), append([]string{"/usr/bin/npc"}, awkwardArgs...))
	if got := systemdQuote(`a "b" 100% $x\{{`); got != `"a \"b\" 100%% $$x\\\x7b\x7b"` {
		t.Errorf("systemdQuote = %s", got)
	}
	if key, _ := serviceScript("windows-service", "x", nil); key != "" {
		t.Error("windows gets a script")
	}
}

func TestParseSysvCmdQuoted(t *testing.T) {
	for script, want := range map[string]string{
		"cmd='/usr/bin/npc'\n":        "/usr/bin/npc",
		"cmd='/opt/my dir/npc'\n":     "/opt/my dir/npc",
		"cmd='/opt/it'\\''s/npc'\n":   "/opt/it's/npc",
		"cmd='/opt/{''{x/npc'\n":      "/opt/{{x/npc",
		"cmd=\"/usr/bin/npc -a=1\"\n": "/usr/bin/npc",
	} {
		if got := parseSysvCmd(script); got != want {
			t.Errorf("parseSysvCmd(%q) = %q, want %q", script, got, want)
		}
	}
}
