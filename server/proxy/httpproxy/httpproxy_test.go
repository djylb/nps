package httpproxy

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestChangeRedirectURL(t *testing.T) {
	s := &HttpProxy{}

	req := &http.Request{
		Host:       "example.com:8080",
		RemoteAddr: "10.0.0.1:52345",
		RequestURI: "/old/path?a=1&b=2",
		URL: &url.URL{
			Path:     "/old/path",
			RawQuery: "a=1&b=2",
		},
		Header: http.Header{
			"X-Forwarded-For": []string{"1.1.1.1", "2.2.2.2"},
		},
	}

	t.Run("replaces template variables", func(t *testing.T) {
		got := s.ChangeRedirectURL(req, "https://${host}/new?from=${request_uri}&xff=${proxy_add_x_forwarded_for}&ip=${remote_ip}")
		want := "https://example.com/new?from=/old/path?a=1&b=2&xff=1.1.1.1, 2.2.2.2, 10.0.0.1&ip=10.0.0.1"
		if got != want {
			t.Fatalf("ChangeRedirectURL() = %q, want %q", got, want)
		}
	})

	t.Run("returns html-unescaped literal when no template", func(t *testing.T) {
		got := s.ChangeRedirectURL(req, " https://static.example.com/a?x=1&amp;y=2 ")
		want := "https://static.example.com/a?x=1&y=2"
		if got != want {
			t.Fatalf("ChangeRedirectURL() = %q, want %q", got, want)
		}
	})
}

func TestChangeHostAndHeader(t *testing.T) {
	s := &HttpProxy{}

	req := &http.Request{
		Host:       "demo.local:8080",
		RemoteAddr: "192.168.1.9:6000",
		RequestURI: "/api/v1?q=ok",
		URL: &url.URL{
			Path:     "/api/v1",
			RawQuery: "q=ok",
		},
		Header: http.Header{
			"Origin":          []string{"http://demo.local:8080"},
			"X-Forwarded-For": []string{"8.8.8.8"},
			"X-Remove-Me":     []string{"to-delete"},
		},
	}

	headerRules := strings.Join([]string{
		"X-Test-IP: ${remote_ip}",
		"X-Test-URI: ${request_uri}",
		"X-Test-XFF: ${proxy_add_x_forwarded_for}",
		"X-Remove-Me: ${unset}",
	}, "\n")

	s.ChangeHostAndHeader(req, "upstream.example.com", headerRules, true)

	if req.Host != "upstream.example.com" {
		t.Fatalf("Host = %q, want %q", req.Host, "upstream.example.com")
	}
	if got := req.Header.Get("Origin"); got != "http://upstream.example.com" {
		t.Fatalf("Origin = %q, want %q", got, "http://upstream.example.com")
	}
	if got := req.Header.Get("X-Test-IP"); got != "192.168.1.9" {
		t.Fatalf("X-Test-IP = %q, want %q", got, "192.168.1.9")
	}
	if got := req.Header.Get("X-Test-URI"); got != "/api/v1?q=ok" {
		t.Fatalf("X-Test-URI = %q, want %q", got, "/api/v1?q=ok")
	}
	if got := req.Header.Get("X-Test-XFF"); got != "8.8.8.8, 192.168.1.9" {
		t.Fatalf("X-Test-XFF = %q, want %q", got, "8.8.8.8, 192.168.1.9")
	}
	if got := req.Header.Get("X-Remove-Me"); got != "" {
		t.Fatalf("X-Remove-Me = %q, want empty", got)
	}
}

func TestChangeResponseHeader(t *testing.T) {
	s := &HttpProxy{}

	t.Run("handles nil response safely", func(t *testing.T) {
		s.ChangeResponseHeader(nil, "X-Test: abc")
	})

	req := &http.Request{
		Method:     http.MethodGet,
		Host:       "resp.example.com:8443",
		RemoteAddr: "127.0.0.1:3456",
		RequestURI: "/hello?foo=bar",
		URL: &url.URL{
			Path:     "/hello",
			RawQuery: "foo=bar",
		},
		TLS: &tls.ConnectionState{},
		Header: http.Header{
			"Origin": []string{"https://origin.example.com"},
		},
	}
	resp := &http.Response{
		Status:        "201 Created",
		StatusCode:    201,
		ContentLength: 99,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Via":          []string{"test-via"},
			"X-Delete-Me":  []string{"bye"},
		},
		Request: req,
	}

	headerRules := strings.Join([]string{
		"X-Resp-Scheme: ${scheme}",
		"X-Resp-Code: ${status_code}",
		"X-Resp-Origin: ${origin}",
		"X-Resp-Date: ${date}",
		"X-Delete-Me: ${unset}",
	}, "\n")
	s.ChangeResponseHeader(resp, headerRules)

	if got := resp.Header.Get("X-Resp-Scheme"); got != "https" {
		t.Fatalf("X-Resp-Scheme = %q, want %q", got, "https")
	}
	if got := resp.Header.Get("X-Resp-Code"); got != "201" {
		t.Fatalf("X-Resp-Code = %q, want %q", got, "201")
	}
	if got := resp.Header.Get("X-Resp-Origin"); got != "https://origin.example.com" {
		t.Fatalf("X-Resp-Origin = %q, want %q", got, "https://origin.example.com")
	}
	if got := resp.Header.Get("X-Resp-Date"); got == "" {
		t.Fatal("X-Resp-Date is empty, want RFC1123 http date")
	}
	if got := resp.Header.Get("X-Delete-Me"); got != "" {
		t.Fatalf("X-Delete-Me = %q, want empty", got)
	}
}
