package acl

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/djylb/geomatch"
	"github.com/djylb/slogx"
)

var log = slogx.Module("acl")

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
		//noinspection GoDfaErrorMayBeNotNil
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
	deny, allow *geomatch.Live
	policy      geomatch.Policy
	opened      openCheck
}

// newDest is the Dest of the lists that are not nil: a deny hit refuses,
// then an allow list admits only what it has.
func newDest(deny, allow *geomatch.Live) *Dest {
	d := &Dest{deny: deny, allow: allow, policy: geomatch.Policy{Priority: geomatch.DenyFirst}}
	if deny != nil {
		d.policy.Deny = deny
	}
	if allow != nil {
		d.policy.Allow = allow
	}
	return d
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
	//noinspection GoDfaErrorMayBeNotNil
	l.SetTag(owner)
	if m == geomatch.ModeAllowlist {
		return newDest(nil, l), ruleError(err)
	}
	return newDest(l, nil), ruleError(err)
}

var (
	// ErrMalformedScope is rule text (a scope) with a syntax error or no rule.
	ErrMalformedScope = errors.New("malformed scope")
	// ErrScopeDenied is a destination the scope does not admit.
	ErrScopeDenied = errors.New("destination outside the scope")
)

// anyTargetRule is what the scope rule * compiles to: every address, by port.
const anyTargetRule = "*:0-65535"

var geoMissingWarned atomic.Bool

// CompileScope compiles rule text: lines of [allow|deny whitespace] rule,
// default allow, ignoring blank lines and those starting with #. Empty text
// is no limit, and nil is returned. Text with no rule or a syntax error is
// ErrMalformedScope. A rule whose geodata is unavailable does not fail: it
// is skipped, a deny that matches nothing and, when the file is missing, an
// allow list that admits everything; this is warned about once.
func CompileScope(text string, owner any) (*Dest, error) {
	if text == "" {
		return nil, nil
	}
	var deny, allow []string
	for line := range strings.Lines(text) {
		s := strings.TrimLeftFunc(strings.TrimRight(line, "\r\n"), unicode.IsSpace)
		if s == "" || s[0] == '#' {
			continue
		}
		list := &allow
		if i := strings.IndexFunc(s, unicode.IsSpace); i > 0 && (s[:i] == "allow" || s[:i] == "deny") {
			if s[:i] == "deny" {
				list = &deny
			}
			s = s[i:]
		}
		rule := strings.TrimSpace(s)
		switch {
		case rule == "":
			return nil, fmt.Errorf("%w: a verb without a rule", ErrMalformedScope)
		case rule == "*":
			rule = anyTargetRule
		case rule[0] == '*' && !strings.HasPrefix(rule, "*:"):
			return nil, fmt.Errorf("%w: %q: wildcards are not accepted", ErrMalformedScope, rule)
		}
		*list = append(*list, rule)
	}
	if len(deny)+len(allow) == 0 {
		return nil, fmt.Errorf("%w: no rule", ErrMalformedScope)
	}
	compile := func(rules []string) (*geomatch.Live, error) {
		if len(rules) == 0 {
			return nil, nil
		}
		l, err := geomatch.NewLive(destCompiler(), rules)
		l.SetTag(owner)
		return l, scopeRuleError(err)
	}
	d, err := compile(deny)
	if err != nil {
		return nil, err
	}
	a, err := compile(allow)
	if err != nil {
		return nil, err
	}
	return newDest(d, a), nil
}

// scopeRuleError is ErrMalformedScope for rejected rules, nil when the only
// fault is unavailable geodata.
func scopeRuleError(err error) error {
	if err == nil {
		return nil
	}
	if errs, ok := errors.AsType[geomatch.RuleErrors](err); ok {
		bad := false
		for _, re := range errs {
			bad = bad || !errors.Is(re.Err, geomatch.ErrGeoData)
		}
		if !bad {
			if geoMissingWarned.CompareAndSwap(false, true) {
				log.Warn("scope rules without geodata are ignored", "err", ruleError(err))
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %w", ErrMalformedScope, ruleError(err))
}

// DeniesName reports whether a deny rule matches addr, a host:port, as it
// is: what can be refused before resolving it.
func (d *Dest) DeniesName(addr string) bool {
	return d != nil && d.deny != nil && d.deny.Matcher().Match(addr)
}

// AllowsResolved decides addr, a host:port, whose host is or resolved to
// ips: a deny of the name or of any IP refuses; then it needs no allow rules
// or an allow of the name or of every IP.
func (d *Dest) AllowsResolved(addr string, ips []netip.Addr) bool {
	if d == nil {
		return true
	}
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return false
	}
	port := uint16(n)
	if d.DeniesName(addr) {
		return false
	}
	if d.deny != nil {
		m := d.deny.Matcher()
		for _, ip := range ips {
			if m.MatchAddrPort(netip.AddrPortFrom(ip.Unmap(), port)) {
				return false
			}
		}
	}
	if d.allow == nil || d.opened.open(d.allow) {
		return true
	}
	m := d.allow.Matcher()
	if m.Match(addr) {
		return true
	}
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !m.MatchAddrPort(netip.AddrPortFrom(ip.Unmap(), port)) {
			return false
		}
	}
	return true
}

// Allows reports whether the destination addr, a host or host:port, may be
// reached.
func (d *Dest) Allows(addr string) bool {
	if d == nil {
		return true
	}
	if d.allow != nil && d.opened.open(d.allow) {
		return !d.DeniesName(addr)
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
