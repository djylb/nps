package acl

import (
	"errors"
	"io/fs"
	"net"
	"sync/atomic"

	"github.com/djylb/geomatch"
)

// Priorities of a source level that has both lists.
const (
	// DenyFirst denies an address on the blacklist even if the whitelist has
	// it, and denies addresses on neither list.
	DenyFirst = int(geomatch.DenyFirst)
	// AllowFirst allows an address on the whitelist even if the blacklist has
	// it, and allows addresses on neither list.
	AllowFirst = int(geomatch.AllowFirst)
)

// NormalizePriority maps unknown priorities to DenyFirst.
func NormalizePriority(p int) int {
	if p == AllowFirst {
		return AllowFirst
	}
	return DenyFirst
}

// Source is the compiled source access policy of one level. A nil Source
// allows every address.
type Source struct {
	black, white *geomatch.Live
	whiteOpen    openCheck
	priority     geomatch.Priority
}

// CompileSource compiles the source lists of a level. Rules that cannot be
// used, for example geoip: rules without geodata, are skipped and named by
// the error; the returned Source applies the others. A whitelist with a rule
// whose geodata file is missing restricts nothing until the file
// is there. owner tags the lists for RefreshGeoData.
func CompileSource(scope Scope, priority int, black, white []string, owner any) (*Source, error) {
	if len(black) == 0 && len(white) == 0 {
		return nil, nil
	}
	c := sourceCompiler(scope)
	s := &Source{priority: geomatch.Priority(NormalizePriority(priority))}
	var errs []error
	for _, list := range []struct {
		rules []string
		live  **geomatch.Live
	}{{black, &s.black}, {white, &s.white}} {
		lines := ruleLines(list.rules)
		if len(lines) == 0 {
			continue
		}
		l, err := geomatch.NewLive(c, lines)
		l.SetTag(owner)
		*list.live = l
		errs = append(errs, err)
	}
	return s, joinRuleErrors(errs...)
}

// Policy returns the geomatch policy of the level.
func (s *Source) Policy() geomatch.Policy {
	if s == nil {
		return geomatch.Policy{}
	}
	p := geomatch.Policy{Priority: s.priority}
	if s.black != nil {
		p.Deny = s.black
	}
	if s.white != nil && !s.whiteOpen.open(s.white) {
		p.Allow = s.white
	}
	return p
}

// Dest is the compiled destination access policy of a tunnel. A nil Dest
// allows every destination.
type Dest struct {
	rules     *geomatch.Live
	policy    geomatch.Policy
	allowlist bool
	opened    openCheck
}

// CompileDest compiles a destination list in mode AclOff, AclWhitelist or
// AclBlacklist (0, 1, 2). An empty whitelist allows nothing; one with a rule
// whose geodata file is missing allows everything until the file
// is there. owner tags the list for RefreshGeoData.
func CompileDest(mode int, rules string, owner any) (*Dest, error) {
	m := geomatch.Mode(mode)
	if m != geomatch.ModeAllowlist && m != geomatch.ModeDenylist {
		return nil, nil
	}
	l, err := geomatch.NewLive(destCompiler(), geomatch.SplitLines(rules))
	l.SetTag(owner)
	return &Dest{rules: l, policy: m.Policy(l), allowlist: m == geomatch.ModeAllowlist}, ruleError(err)
}

// Allows reports whether the destination addr, a host or host:port, may be
// reached.
func (d *Dest) Allows(addr string) bool {
	if d == nil {
		return true
	}
	if d.allowlist && d.opened.open(d.rules) {
		return true
	}
	return d.policy.Allows(addr)
}

// openCheck remembers, for the Matcher a list last had, whether the list
// lacks a geodata file, so that queries need not look at its errors.
type openCheck struct {
	last atomic.Pointer[openState]
}

type openState struct {
	m    *geomatch.Matcher
	open bool
}

// open reports whether a rule of l was skipped because its geodata file is
// missing or not configured. Other faults, such as a code the file does not have,
// leave the list as it is.
func (c *openCheck) open(l *geomatch.Live) bool {
	m := l.Matcher()
	if st := c.last.Load(); st != nil && st.m == m {
		return st.open
	}
	err := l.Err()
	st := &openState{m: m, open: errors.Is(err, fs.ErrNotExist) || errors.Is(err, geomatch.ErrNoGeoData)}
	c.last.Store(st)
	return st.open
}

// Allows reports whether every level allows addr. Levels are the global,
// client and tunnel or host policies; nil levels allow everything.
func Allows(addr net.Addr, global, client, tunnel *Source) bool {
	if global == nil && client == nil && tunnel == nil {
		return true
	}
	return levels(nil, global, client, tunnel).AllowsNetAddr(addr)
}

// AllowsString is Allows for an address given as text, such as host:port.
func AllowsString(addr string, global, client, tunnel *Source) bool {
	if global == nil && client == nil && tunnel == nil {
		return true
	}
	return levels(nil, global, client, tunnel).Allows(addr)
}

// Decide is Allows with the level and rule that decided. names name the
// global, client and tunnel levels for the decision's text.
func Decide(addr net.Addr, names []string, global, client, tunnel *Source) geomatch.Decision {
	if global == nil && client == nil && tunnel == nil {
		return geomatch.Decision{Allowed: true, Level: -1}
	}
	return levels(names, global, client, tunnel).DecideNetAddr(addr)
}

// DecideString is Decide for an address given as text, such as host:port.
func DecideString(addr string, names []string, global, client, tunnel *Source) geomatch.Decision {
	if global == nil && client == nil && tunnel == nil {
		return geomatch.Decision{Allowed: true, Level: -1}
	}
	return levels(names, global, client, tunnel).Decide(addr)
}

// levels builds the global, client and tunnel policies in order, each named
// by names for the decisions.
func levels(names []string, global, client, tunnel *Source) geomatch.Levels {
	ps := []geomatch.Policy{global.Policy(), client.Policy(), tunnel.Policy()}
	for i := range ps {
		if i < len(names) {
			ps[i].Name = names[i]
		}
	}
	return geomatch.Levels{Policies: ps}
}

// joinRuleErrors merges the rule errors of several lists into one error.
func joinRuleErrors(errs ...error) error {
	var all geomatch.RuleErrors
	var other []error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if re, ok := errors.AsType[geomatch.RuleErrors](err); ok {
			all = append(all, re...)
		} else {
			other = append(other, err)
		}
	}
	if len(other) > 0 {
		return errors.Join(append(other, ruleErrorOrNil(all))...)
	}
	return ruleErrorOrNil(all)
}

func ruleErrorOrNil(errs geomatch.RuleErrors) error {
	if len(errs) == 0 {
		return nil
	}
	return &RuleError{errs: errs}
}
