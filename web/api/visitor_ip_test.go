package api

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

type visitorContext struct {
	Context
	remote string
	header map[string]string
}

func (c visitorContext) RemoteAddr() string            { return c.remote }
func (c visitorContext) RequestHeader(k string) string { return c.header[k] }

func TestVisitorIP(t *testing.T) {
	trusting := &servercfg.Snapshot{Auth: servercfg.AuthConfig{AllowXRealIP: true, TrustedProxyIPs: "10.0.0.1", HTTPOnlyPass: "pass"}}
	for _, tc := range []struct {
		name   string
		cfg    *servercfg.Snapshot
		remote string
		header map[string]string
		want   string
	}{
		{"trusted proxy", trusting, "10.0.0.1:1234", map[string]string{"X-Real-IP": "203.0.113.7"}, "203.0.113.7"},
		{"trusted proxy with port", trusting, "10.0.0.1:1234", map[string]string{"X-Real-IP": "[2001:db8::7]:80"}, "2001:db8::7"},
		{"trusted proxy invalid value", trusting, "10.0.0.1:1234", map[string]string{"X-Real-IP": "evil<script>"}, "10.0.0.1"},
		{"trusted proxy no header", trusting, "10.0.0.1:1234", nil, "10.0.0.1"},
		{"untrusted source", trusting, "198.51.100.2:1234", map[string]string{"X-Real-IP": "203.0.113.7"}, "198.51.100.2"},
		{"x-real-ip disabled", &servercfg.Snapshot{Auth: servercfg.AuthConfig{TrustedProxyIPs: "10.0.0.1"}}, "10.0.0.1:1234", map[string]string{"X-Real-IP": "203.0.113.7"}, "10.0.0.1"},
		{"http-only pass", trusting, "198.51.100.2:1234", map[string]string{"X-NPS-Http-Only": "pass", "X-Real-IP": "203.0.113.7"}, "203.0.113.7"},
		{"http-only pass invalid value", trusting, "198.51.100.2:1234", map[string]string{"X-NPS-Http-Only": "pass", "X-Real-IP": "nope"}, "198.51.100.2"},
		{"wrong http-only pass", trusting, "198.51.100.2:1234", map[string]string{"X-NPS-Http-Only": "bad", "X-Real-IP": "203.0.113.7"}, "198.51.100.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisitorIP(visitorContext{remote: tc.remote, header: tc.header}, tc.cfg); got != tc.want {
				t.Fatalf("VisitorIP = %q, want %q", got, tc.want)
			}
		})
	}
}
