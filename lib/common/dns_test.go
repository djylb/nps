package common

import (
	"net"
	"testing"
)

func TestGetCustomDNSDefaultWhenUnset(t *testing.T) {
	oldCustom := customDnsAddr
	customDnsAddr = ""
	t.Cleanup(func() {
		customDnsAddr = oldCustom
	})

	if got := GetCustomDNS(); got != "8.8.8.8:53" {
		t.Fatalf("GetCustomDNS() = %q, want %q", got, "8.8.8.8:53")
	}
}

func TestSetCustomDNSFormatsAddress(t *testing.T) {
	oldCustom := customDnsAddr
	oldResolver := net.DefaultResolver
	t.Cleanup(func() {
		customDnsAddr = oldCustom
		net.DefaultResolver = oldResolver
	})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ipv4 without port", input: "1.1.1.1", want: "1.1.1.1:53"},
		{name: "ipv4 with port", input: "1.1.1.1:5353", want: "1.1.1.1:5353"},
		{name: "ipv6 raw", input: "2001:4860:4860::8888", want: "[2001:4860:4860::8888]:53"},
		{name: "ipv6 bracketed without port", input: "[2001:4860:4860::8844]", want: "[2001:4860:4860::8844]:53"},
		{name: "ipv6 bracketed with port", input: "[2001:4860:4860::8844]:5353", want: "[2001:4860:4860::8844]:5353"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetCustomDNS(tc.input)
			if got := GetCustomDNS(); got != tc.want {
				t.Fatalf("GetCustomDNS() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetCustomDNSEmptyInputKeepsPreviousValue(t *testing.T) {
	oldCustom := customDnsAddr
	oldResolver := net.DefaultResolver
	t.Cleanup(func() {
		customDnsAddr = oldCustom
		net.DefaultResolver = oldResolver
	})

	SetCustomDNS("9.9.9.9")
	gotBefore := GetCustomDNS()
	SetCustomDNS("")
	if gotAfter := GetCustomDNS(); gotAfter != gotBefore {
		t.Fatalf("empty SetCustomDNS should keep previous value: before=%q after=%q", gotBefore, gotAfter)
	}
}
