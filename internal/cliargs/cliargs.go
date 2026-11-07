// Package cliargs prepares the command line of nps and npc for pflag. A
// service keeps the arguments it was installed with, so an update must not
// stop because an argument was renamed or removed since: an argument of an
// earlier version is translated to what replaces it or dropped with a
// warning (the Rules NPC and NPS), and any other unknown argument is
// dropped with a warning, rather than failing the start.
package cliargs

import (
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// A Rule says what an argument of an earlier version becomes.
type Rule struct {
	// Name is the flag name as earlier versions spelt it, without dashes.
	Name string
	// Bool marks a boolean flag: it never takes the argument after it as
	// its value, as -name, -name=true and -name=false.
	Bool bool
	// Translate returns the arguments that replace the flag given value
	// ("" when none was given), and the warning to log about it. They are
	// placed after the other flags, so they win over what those set, as
	// earlier versions let the old flag win.
	Translate func(value string, hasValue bool) (args []string, warning string)
}

// Prepare returns args, without the program name, ready for fs.Parse, and
// the warnings to log once logging is set up: single-dash long flags such
// as -server become --server; a flag fs does not define is handled by its
// rule in rules or else left out. A flag left out that is not Bool and is
// written without "=" also takes the argument after it, its value, unless
// that starts with "-" or is one of commands. Arguments after "--" are kept
// as they are.
func Prepare(fs *pflag.FlagSet, args []string, rules []Rule, commands ...string) (out, warnings []string) {
	long := map[string]bool{}
	short := map[string]bool{}
	fs.VisitAll(func(f *pflag.Flag) {
		long[normalize(f.Name)] = true
		if f.Shorthand != "" {
			short[f.Shorthand] = true
		}
	})
	out = make([]string, 0, len(args))
	var added, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = args[i:]
			break
		}
		if len(a) < 2 || a[0] != '-' {
			out = append(out, a)
			continue
		}
		double := strings.HasPrefix(a, "--")
		body := strings.TrimLeft(a, "-")
		name, value, hasValue := strings.Cut(body, "=")
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
		rule := findRule(rules, name)
		if !hasValue && (rule == nil || !rule.Bool) && i+1 < len(args) {
			next := args[i+1]
			if !strings.HasPrefix(next, "-") && !slices.Contains(commands, next) {
				a += " " + next
				value, hasValue = next, true
				i++
			}
		}
		if rule == nil {
			warnings = append(warnings, "unknown argument \""+a+"\" ignored")
			continue
		}
		extra, warning := rule.Translate(value, hasValue)
		added = append(added, extra...)
		if warning != "" {
			warnings = append(warnings, "\""+a+"\": "+warning)
		}
	}
	out = append(out, added...)
	return append(out, rest...), warnings
}

func findRule(rules []Rule, name string) *Rule {
	name = normalize(name)
	for i := range rules {
		if normalize(rules[i].Name) == name {
			return &rules[i]
		}
	}
	return nil
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
