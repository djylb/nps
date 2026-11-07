package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/acl"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
)

func newTestHTTPTask(port int, mode string) *model.Tunnel {
	task := newTestSocks5Task(port, mode)
	task.HttpProxy = true
	return task
}

func dialHTTPProxy(t *testing.T, port int) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	return c, bufio.NewReader(c)
}

// roundTrip writes a raw request and reads the response with its body.
func roundTrip(t *testing.T, c net.Conn, br *bufio.Reader, raw string) (*http.Response, string) {
	t.Helper()
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

func basic(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// echoBackend answers with the method, the request body and the forwarding
// fields it received.
func echoBackend(t *testing.T) string {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Got-XFF", strings.Join(r.Header.Values("X-Forwarded-For"), ","))
		w.Header().Set("X-Got-XFP", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Got-Proxy-Auth", r.Header.Get("Proxy-Authorization"))
		_, _ = io.WriteString(w, r.Method+" "+r.URL.Path+" "+string(body))
	}))
	t.Cleanup(backend.Close)
	return strings.TrimPrefix(backend.URL, "http://")
}

func TestHTTPProxyConnectRelaysThroughTunnel(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "httpProxy")
	bridge := &echoBridge{links: make(chan *conn.Link, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	c, br := dialHTTPProxy(t, port)
	// Early data after the head belongs to the tunnel.
	if _, err := io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\nearly"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT reply = %v, %v", resp, err)
	}
	link := <-bridge.links
	if link.Host != "example.com:443" || link.ConnType != "tcp" {
		t.Fatalf("link = %s %s", link.ConnType, link.Host)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "early" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	if _, err := io.WriteString(c, "later"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "later" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	_ = c.Close()
	waitFlow(t, task.Flow, "task")
	waitFlow(t, task.Client.Flow, "client")
}

func TestHTTPProxyConnectDestinationDenied(t *testing.T) {
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "mixProxy")
	task.DestAclMode = model.AclWhitelist
	task.DestAclRules = "192.0.2.1"
	task.CompileDestACL()
	bridge := &echoBridge{links: make(chan *conn.Link, 1)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	c, br := dialHTTPProxy(t, port)
	if _, err := io.WriteString(c, "CONNECT 198.51.100.1:443 HTTP/1.1\r\nHost: 198.51.100.1:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("CONNECT reply = %v, %v, want 403", resp, err)
	}
	select {
	case l := <-bridge.links:
		t.Fatalf("link opened to %s", l.Host)
	default:
	}

	c, br = dialHTTPProxy(t, port)
	resp, _ = roundTrip(t, c, br, "GET http://198.51.100.1/ HTTP/1.1\r\nHost: 198.51.100.1\r\n\r\n")
	if resp.StatusCode != 403 {
		t.Fatalf("GET status = %d, want 403", resp.StatusCode)
	}
}

func TestHTTPProxyForwardsKeepAliveAndBodies(t *testing.T) {
	host := echoBackend(t)
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "httpProxy")
	bridge := &localDialBridge{links: make(chan *conn.Link, 16)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	c, br := dialHTTPProxy(t, port)
	for _, tc := range []struct{ raw, want string }{
		{"GET http://" + host + "/a HTTP/1.1\r\nHost: " + host + "\r\n\r\n", "GET /a "},
		{"POST http://" + host + "/b HTTP/1.1\r\nHost: " + host + "\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n2\r\nde\r\n0\r\n\r\n", "POST /b abcde"},
		{"POST http://" + host + "/c HTTP/1.1\r\nHost: " + host + "\r\nContent-Length: 4\r\n\r\nwxyz", "POST /c wxyz"},
	} {
		resp, body := roundTrip(t, c, br, tc.raw)
		if resp.StatusCode != 200 || body != tc.want {
			t.Fatalf("response = %d %q, want 200 %q", resp.StatusCode, body, tc.want)
		}
	}
	link := <-bridge.links
	if link.Host != host || link.ConnType != "tcp" {
		t.Fatalf("link = %s %s", link.ConnType, link.Host)
	}
	_ = c.Close()
	waitFlow(t, task.Flow, "task")
	waitFlow(t, task.Client.Flow, "client")
}

func TestHTTPProxyAuthentication(t *testing.T) {
	host := echoBackend(t)
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "httpProxy")
	task.Client.Cnf.U, task.Client.Cnf.P = "user", "pass"
	task.MultiAccount = &model.MultiAccount{AccountMap: map[string]string{"alt": "secret"}}
	bridge := &localDialBridge{links: make(chan *conn.Link, 16)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	get := "GET http://" + host + "/ HTTP/1.1\r\nHost: " + host + "\r\n"
	for _, tc := range []struct {
		name, fields string
		want         int
	}{
		{"none", "", 407},
		{"wrong", "Proxy-Authorization: " + basic("user", "bad") + "\r\n", 407},
		{"proxy header", "Proxy-Authorization: " + basic("user", "pass") + "\r\n", 200},
		{"authorization fallback", "Authorization: " + basic("user", "pass") + "\r\n", 200},
		{"proxy header wins", "Proxy-Authorization: " + basic("user", "bad") + "\r\nAuthorization: " + basic("user", "pass") + "\r\n", 407},
		{"multi account", "Proxy-Authorization: " + basic("alt", "secret") + "\r\n", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, br := dialHTTPProxy(t, port)
			resp, _ := roundTrip(t, c, br, get+tc.fields+"Connection: close\r\n\r\n")
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 407 && !strings.HasPrefix(resp.Header.Get("Proxy-Authenticate"), "Basic realm=") {
				t.Fatalf("Proxy-Authenticate = %q", resp.Header.Get("Proxy-Authenticate"))
			}
			if tc.want == 200 && resp.Header.Get("X-Got-Proxy-Auth") != "" {
				t.Fatal("Proxy-Authorization was forwarded to the target")
			}
		})
	}

	c, br := dialHTTPProxy(t, port)
	if _, err := io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != 407 {
		t.Fatalf("CONNECT without credentials = %v, %v, want 407", resp, err)
	}
}

func TestHTTPProxyForwardingFields(t *testing.T) {
	host := echoBackend(t)
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "httpProxy")
	bridge := &localDialBridge{links: make(chan *conn.Link, 16)}
	server, errCh := startTestTunnelModeServer(t, task, bridge)
	defer stopTestTunnelModeServer(t, server, errCh)

	c, br := dialHTTPProxy(t, port)
	resp, _ := roundTrip(t, c, br, "GET http://"+host+"/ HTTP/1.1\r\nHost: "+host+
		"\r\nX-Forwarded-For: 203.0.113.9\r\nX-Forwarded-Proto: https\r\n\r\n")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Got-XFF"); got != "" {
		t.Fatalf("target saw X-Forwarded-For %q, want none", got)
	}
	if got := resp.Header.Get("X-Got-XFP"); got != "https" {
		t.Fatalf("target saw X-Forwarded-Proto %q, want https", got)
	}
}

// failingBridge fails every link, or opens links that close at once.
type failingBridge struct {
	err error
}

func (b *failingBridge) Session(int) conn.ClientSession { return b }

func (b *failingBridge) OpenLink(*conn.Link, int) (net.Conn, error) {
	if b.err != nil {
		return nil, b.err
	}
	local, peer := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, peer)
	}()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = peer.Close()
	}()
	return local, nil
}

func (b *failingBridge) IsServer() bool { return true }

func (b *failingBridge) GlobalSource() *acl.Source { return nil }

func (b *failingBridge) CliProcess(*conn.Conn, string) {}

func TestHTTPProxyUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bridge  NetBridge
		expired bool
		want    int
	}{
		{"link fails", &failingBridge{err: io.ErrClosedPipe}, false, 502},
		{"target closes", &failingBridge{}, false, upstreamClosedStatus},
		{"flow expired", &localDialBridge{links: make(chan *conn.Link, 16)}, true, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := freeTCPPort(t)
			task := newTestHTTPTask(port, "httpProxy")
			if tc.expired {
				task.Flow = &model.Flow{TimeLimit: time.Now().Add(-time.Hour)}
			}
			server, errCh := startTestTunnelModeServer(t, task, tc.bridge)
			defer stopTestTunnelModeServer(t, server, errCh)
			host := echoBackend(t)
			c, br := dialHTTPProxy(t, port)
			resp, _ := roundTrip(t, c, br, "GET http://"+host+"/ HTTP/1.1\r\nHost: "+host+"\r\n\r\n")
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func (*failingBridge) LinkCert() *tls.Certificate { return &tls.Certificate{} }

// upstreamClosedStatus answers a target that closes without a response.
const upstreamClosedStatus = 502

func TestHTTPProxyIdleClientConnectionEnds(t *testing.T) {
	host := echoBackend(t)
	port := freeTCPPort(t)
	task := newTestHTTPTask(port, "httpProxy")
	bridge := &localDialBridge{links: make(chan *conn.Link, 16)}
	server := NewTunnelModeServer(ProcessMix, bridge, task, false)
	if server.httpProxy.IdleTimeout != 30*time.Second {
		t.Fatalf("IdleTimeout = %v, want 30s", server.httpProxy.IdleTimeout)
	}
	server.httpProxy.IdleTimeout = 200 * time.Millisecond
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()
	waitForTCP(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	defer stopTestTunnelModeServer(t, server, errCh)

	c, br := dialHTTPProxy(t, port)
	resp, _ := roundTrip(t, c, br, "GET http://"+host+"/ HTTP/1.1\r\nHost: "+host+"\r\n\r\n")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	start := time.Now()
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("read on idle connection = %v, want EOF", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("idle connection ended after %v", d)
	}
}
