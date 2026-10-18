package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/file"
)

// ErrInvalidAccessList is wrapped by the errors that report rejected rules
// of an access list; the message names the list and the lines.
var ErrInvalidAccessList = errors.New("invalid access list rules")

// AccessListInput holds the source access lists of a level as entered in
// the web interface, one rule per line.
type AccessListInput struct {
	Priority int
	Black    string
	White    string
	// RegisteredOnly additionally requires visitor addresses to be
	// registered; the global settings ignore it.
	RegisteredOnly bool
}

type accessListError struct {
	list string
	err  error
}

func (e *accessListError) Error() string {
	return fmt.Sprintf("invalid rules in the %s: %v", e.list, e.err)
}

func (e *accessListError) Unwrap() []error { return []error{ErrInvalidAccessList, e.err} }

// accessLists are lists that passed validation, in canonical form.
type accessLists struct {
	priority       int
	black, white   []string
	scope          acl.Scope
	registeredOnly bool
}

// validate checks the rules and returns them in canonical form.
func (in AccessListInput) validate(scope acl.Scope) (accessLists, error) {
	black, err := acl.NormalizeSource(scope, splitRuleLines(in.Black))
	if err != nil {
		return accessLists{}, &accessListError{list: "blacklist", err: err}
	}
	white, err := acl.NormalizeSource(scope, splitRuleLines(in.White))
	if err != nil {
		return accessLists{}, &accessListError{list: "whitelist", err: err}
	}
	return accessLists{priority: in.Priority, black: black, white: white, scope: scope, registeredOnly: in.RegisteredOnly && scope == acl.ScopeUser}, nil
}

// applyTo stores the lists in l, compiled.
func (v accessLists) applyTo(l *file.SourceLists, owner string) {
	l.RegisteredOnly = v.registeredOnly
	l.SetSourceLists(v.priority, v.black, v.white, v.scope, owner)
}

// normalizeDestRules validates destination rules and returns them in
// canonical form.
func normalizeDestRules(raw string) (string, error) {
	rules, err := acl.NormalizeDest(raw)
	if err != nil {
		return "", &accessListError{list: "destination list", err: err}
	}
	return rules, nil
}

func splitRuleLines(text string) []string {
	return strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
}

// joinRuleLines renders a list for a text area.
func joinRuleLines(rules []string) string {
	return strings.Join(rules, "\n")
}
