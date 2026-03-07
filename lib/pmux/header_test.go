package pmux

import "testing"

func TestParseHostHeader(t *testing.T) {
	tests := []struct {
		name string
		line []byte
		want string
		ok   bool
	}{
		{name: "standard", line: []byte("Host: example.com"), want: "example.com", ok: true},
		{name: "lowercase", line: []byte("host: api.example.com:8080"), want: "api.example.com:8080", ok: true},
		{name: "mixedcase and spaces", line: []byte("HoSt:   test.local  "), want: "test.local", ok: true},
		{name: "not host", line: []byte("User-Agent: curl"), want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseHostHeader(tt.line)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("parseHostHeader(%q) = (%q, %v), want (%q, %v)", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}
