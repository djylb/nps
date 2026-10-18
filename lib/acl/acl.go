// Package acl compiles and evaluates the access lists of nps on top of
// geomatch: the source lists (who may connect) of the global, client and
// tunnel or host levels, and the destination list of a proxy tunnel.
package acl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/djylb/geomatch"
)

// geo is the geodata shared by every compiled list, so that geo entries are
// loaded once for all of them.
var geo = geomatch.NewGeoData(geomatch.GeoFiles{})

// SetGeoData sets the geoip and geosite files. Lists compiled from other
// files report stale and are refreshed by Refresh. ext-ip: rules of the
// global lists resolve relative names next to the geoip file.
func SetGeoData(ipFile, siteFile string) {
	geo.SetFiles(geomatch.GeoFiles{IP: ipFile, Site: siteFile})
}

// GeoFiles returns the geoip and geosite files.
func GeoFiles() (ipFile, siteFile string) {
	f := geo.Files()
	return f.IP, f.Site
}

// GeoGeneration checks the geodata files and returns a number that grows
// whenever one of them changed, so that callers refresh their lists only
// then.
func GeoGeneration() uint64 {
	return geo.Check()
}

// Scope tells who may write a list, which bounds what it may contain.
type Scope uint8

const (
	// ScopeGlobal lists are written by the administrator only.
	ScopeGlobal Scope = iota
	// ScopeUser lists may be written by users.
	ScopeUser
)

// Limits of the lists users may write.
const (
	maxRuleLen     = 256
	maxUserRegexps = 50
)

// DefaultMaxRules is the default limit on the rules of a list users may
// write.
const DefaultMaxRules = 1000

// maxRules limits the rules of a list users may write; 0 means no limit.
var maxRules = DefaultMaxRules

// SetMaxRules sets the limit on the rules of each list users may write, the
// client, tunnel, host and destination lists; 0 means no limit. It must be
// called before any list is compiled.
func SetMaxRules(n int) {
	if n < 0 {
		n = 0
	}
	maxRules = n
}

func sourceCompiler(s Scope) geomatch.Compiler {
	if s == ScopeGlobal {
		return geomatch.Compiler{GeoData: geo, Kinds: geomatch.IPKinds, ExtFiles: geomatch.ExtLocal}
	}
	return geomatch.Compiler{
		GeoData:  geo,
		Kinds:    geomatch.IPKinds,
		ExtFiles: geomatch.ExtNone,
		Limits:   geomatch.Limits{MaxRules: maxRules, MaxRuleLen: maxRuleLen},
	}
}

func destCompiler() geomatch.Compiler {
	return geomatch.Compiler{
		GeoData:  geo,
		Kinds:    geomatch.AllKinds.Without(geomatch.RuleExt),
		ExtFiles: geomatch.ExtNone,
		Limits:   geomatch.Limits{MaxRules: maxRules, MaxRuleLen: maxRuleLen, MaxRegexps: maxUserRegexps},
	}
}

// RuleError reports the rules of a list that were rejected, by line.
type RuleError struct {
	errs geomatch.RuleErrors
}

func (e *RuleError) Error() string {
	lines := make([]string, 0, len(e.errs))
	for _, re := range e.errs {
		lines = append(lines, fmt.Sprintf("line %d %q: %v", re.Index+1, re.Rule, re.Err))
	}
	return strings.Join(lines, "; ")
}

func (e *RuleError) Unwrap() error { return e.errs }

func ruleError(err error) error {
	var errs geomatch.RuleErrors
	if errors.As(err, &errs) && len(errs) > 0 {
		return &RuleError{errs: errs}
	}
	return err
}

// normalize returns the canonical form of the rules in lines, without
// duplicates, and an error naming the rules it rejected by line; they are
// left out of the result.
func normalize(c geomatch.Compiler, lines []string) ([]string, error) {
	out, err := c.Normalize(lines)
	return out, ruleError(err)
}

// NormalizeSource returns the canonical form of the source rules, without
// duplicates. The error names the rules that were rejected; they are left
// out of the result.
func NormalizeSource(scope Scope, rules []string) ([]string, error) {
	lines := make([]string, 0, len(rules))
	for _, r := range rules {
		lines = append(lines, strings.Split(strings.ReplaceAll(r, "\r", "\n"), "\n")...)
	}
	return normalize(sourceCompiler(scope), lines)
}

// NormalizeDest returns the canonical form of destination rules, one per
// line. The error names the rules that were rejected; they are left out.
func NormalizeDest(raw string) (string, error) {
	out, err := normalize(destCompiler(), geomatch.SplitLines(raw))
	return strings.Join(out, "\n"), err
}
