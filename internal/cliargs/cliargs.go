// Package cliargs prepares the command line of nps and npc for pflag. A
// service keeps the arguments it was installed with, so an update must not
// stop because an argument was renamed or removed since: such arguments are
// dropped with a warning rather than failing the start.
package cliargs

import (
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// Prepare returns args, without the program name, ready for fs.Parse:
// single-dash long flags such as -server become --server, and flags fs does
// not define are left out and listed in ignored. A dropped flag written
// without "=" also takes the argument after it, its value, unless that
// starts with "-" or is one of commands. Arguments after "--" are kept as
// they are.
func Prepare(fs *pflag.FlagSet, args []string, commands ...string) (out, ignored []string) {
	long := map[string]bool{}
	short := map[string]bool{}
	fs.VisitAll(func(f *pflag.Flag) {
		long[normalize(f.Name)] = true
		if f.Shorthand != "" {
			short[f.Shorthand] = true
		}
	})
	out = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...), ignored
		}
		if len(a) < 2 || a[0] != '-' {
			out = append(out, a)
			continue
		}
		double := strings.HasPrefix(a, "--")
		body := strings.TrimLeft(a, "-")
		name, _, hasValue := strings.Cut(body, "=")
		switch {
		case long[normalize(name)]:
			if !double {
				a = "--" + body
			}
			out = append(out, a)
			continue
		case !double && shorthands(name, short):
			out = append(out, a)
			continue
		}
		if !hasValue && i+1 < len(args) {
			next := args[i+1]
			if !strings.HasPrefix(next, "-") && !slices.Contains(commands, next) {
				a += " " + next
				i++
			}
		}
		ignored = append(ignored, a)
	}
	return out, ignored
}

// shorthands reports whether name is one or more defined shorthand letters,
// as in -v or -abc.
func shorthands(name string, short map[string]bool) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !short[string(r)] {
			return false
		}
	}
	return true
}

// normalize matches how nps and npc name flags: "-" and "." become "_".
func normalize(s string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(s)
}
