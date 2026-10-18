package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/file"
)

func TestAccessListInputValidate(t *testing.T) {
	v, err := AccessListInput{Priority: 1, Black: "1.2.3.4\r\n\r\n10.1.2.3/8", White: "geoip:private\n# note"}.validate(acl.ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	l := new(file.SourceLists)
	v.applyTo(l, "test")
	if l.IpListPriority != acl.AllowFirst || !slices.Equal(l.BlackIpList, []string{"1.2.3.4", "10.0.0.0/8"}) || !slices.Equal(l.WhiteIpList, []string{"geoip:private"}) {
		t.Fatalf("lists = %+v", l)
	}

	_, err = AccessListInput{White: "1.2.3.4\nexample.com"}.validate(acl.ScopeUser)
	if !errors.Is(err, ErrInvalidAccessList) || !strings.Contains(err.Error(), "whitelist") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want the whitelist line 2 rejected", err)
	}
}

func TestNormalizeDestRules(t *testing.T) {
	got, err := normalizeDestRules("full:Example.com\n*.a.com\nads")
	if err != nil || got != "full:example.com\n*.a.com\nkeyword:ads" {
		t.Fatalf("normalizeDestRules = %q, %v", got, err)
	}
	if _, err := normalizeDestRules("regexp:("); !errors.Is(err, ErrInvalidAccessList) {
		t.Fatalf("invalid regexp: err = %v", err)
	}
}
