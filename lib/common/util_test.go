package common

import "testing"

func TestDomainCheck(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{name: "plain domain", input: "example.com", valid: true},
		{name: "http domain", input: "http://example.com", valid: true},
		{name: "https domain with path", input: "https://example.com/path", valid: true},
		{name: "invalid ip", input: "127.0.0.1", valid: false},
		{name: "invalid string", input: "not_a_domain", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DomainCheck(tc.input); got != tc.valid {
				t.Fatalf("DomainCheck(%q) = %v, want %v", tc.input, got, tc.valid)
			}
		})
	}
}
