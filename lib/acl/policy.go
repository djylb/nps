package acl

import (
	"errors"
	"net"

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
	priority     geomatch.Priority
}

// CompileSource compiles the source lists of a level. Rules that cannot be
// used, for example geoip: rules without geodata, are skipped and named by
// the error; the returned Source applies the others.
func CompileSource(scope Scope, priority int, black, white []string) (*Source, error) {
	if len(black) == 0 && len(white) == 0 {
		return nil, nil
	}
	c := sourceCompiler(scope)
	s := &Source{priority: geomatch.Priority(NormalizePriority(priority))}
	var errs []error
	if len(black) > 0 {
		l, err := geomatch.NewLive(c, black)
		s.black = l
		errs = append(errs, err)
	}
	if len(white) > 0 {
		l, err := geomatch.NewLive(c, white)
		s.white = l
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
	if s.white != nil {
		p.Allow = s.white
	}
	return p
}

// Refresh recompiles the lists whose geodata files changed. It returns the
// rules the new data cannot serve; the lists then keep their previous rules.
func (s *Source) Refresh() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, l := range []*geomatch.Live{s.black, s.white} {
		if l != nil {
			if _, err := l.Refresh(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return joinRuleErrors(errs...)
}

// Dest is the compiled destination access policy of a tunnel. A nil Dest
// allows every destination.
type Dest struct {
	rules  *geomatch.Live
	policy geomatch.Policy
}

// CompileDest compiles a destination list in mode AclOff, AclWhitelist or
// AclBlacklist (0, 1, 2). An empty whitelist allows nothing.
func CompileDest(mode int, rules string) (*Dest, error) {
	m := geomatch.Mode(mode)
	if m != geomatch.ModeAllowlist && m != geomatch.ModeDenylist {
		return nil, nil
	}
	l, err := geomatch.NewLive(destCompiler(), geomatch.SplitLines(rules))
	return &Dest{rules: l, policy: m.Policy(l)}, ruleError(err)
}

// Allows reports whether the destination addr, a host or host:port, may be
// reached.
func (d *Dest) Allows(addr string) bool {
	if d == nil {
		return true
	}
	return d.policy.Allows(addr)
}

// Refresh recompiles the list when its geodata files changed.
func (d *Dest) Refresh() error {
	if d == nil {
		return nil
	}
	_, err := d.rules.Refresh()
	return ruleError(err)
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
		var re geomatch.RuleErrors
		if errors.As(err, &re) {
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
