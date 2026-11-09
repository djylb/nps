package bridge

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/servercfg"
)

// dialHTTP connects a client over server, an http or https candidate, within
// timeout per attempt.
func (f *mixFixture) dialHTTP(tb testing.TB, server string, exit coreapi.Exit, timeout time.Duration) (coreapi.ClientSession, error) {
	tb.Helper()
	cands, err := connprofile.Parse(server, "")
	if err != nil {
		tb.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "npc/9.8.7 test/x",
		Key: key, Exit: exit, Timeout: timeout}).Dial(ctx)
	if err != nil {
		return nil, err
	}
	tb.Cleanup(func() { _ = s.Close(); <-s.Done() })
	return s, nil
}

func (f *mixFixture) mustJoin(t *testing.T, server string) {
	t.Helper()
	if _, err := f.dialHTTP(t, server, client.NewCoreExit("", 0), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	f.waitConnected(t)
}

func httpMix(t *testing.T) *mixFixture {
	t.Helper()
	return mixOn(t, mixVirtual, servercfg.NetworkConfig{MixLegacy: true})
}

// Every kind on http and https goes online and carries a large stream
// unchanged (visit compares the echo).
func TestHTTPCarrierTunnels(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		for _, kind := range []string{"streaming", "split", "websocket"} {
			t.Run(scheme+"/"+kind, func(t *testing.T) {
				f := httpMix(t)
				f.mustJoin(t, scheme+"://"+f.addr+"/ws?kinds="+kind)
				addr, _ := f.startTunnel(t, echoListener(t), "")
				visit(t, addr, 8<<20)
			})
		}
	}
}

// The default kinds connect without any kinds parameter.
func TestHTTPCarrierDefault(t *testing.T) {
	for _, s := range []string{"http://", "https://"} {
		t.Run(s, func(t *testing.T) {
			f := httpMix(t)
			f.mustJoin(t, s+f.addr+"/ws")
		})
	}
}

// WebSocket and HTTP carriers are told apart on the same path.
func TestHTTPCarrierSamePathAsWebSocket(t *testing.T) {
	f := httpMix(t)
	f.dialCore(t, "ws://"+f.addr+"/ws", true)
	f.dialCore(t, "http://"+f.addr+"/ws?kinds=streaming", true)
	f.dialCore(t, "ws://"+f.addr+"/ws", true)
}

func TestHTTPCarrierFallback(t *testing.T) {
	f := httpMix(t)
	for name, req := range map[string]string{
		"plain get":          plainGET,
		"get on bridge path": "GET /ws HTTP/1.1\r\nHost: x\r\n\r\n",
		"post with a length": "POST /ws HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello",
		"streamed junk post": "POST /ws HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n12\r\nhello hello hello!\r\n0\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			f.raw(t, req)
			select {
			case got := <-f.fallback.got:
				if !strings.HasPrefix(got, strings.SplitN(req, " ", 2)[0]) {
					t.Fatalf("fallback got %q", got)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("did not reach the fallback")
			}
		})
	}
}

// reverseProxy serves the bridge behind httputil.ReverseProxy; buffer reads a
// request body whole before forwarding it, as a buffering front does.
func reverseProxy(t *testing.T, addr string, buffer bool) *httptest.Server {
	t.Helper()
	target, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	p := httputil.NewSingleHostReverseProxy(target)
	p.ErrorLog = nil
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if buffer {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(b)))
			r.ContentLength = int64(len(b))
		} else if !strings.EqualFold(r.Header.Get("Connection"), "upgrade") {
			_ = http.NewResponseController(w).EnableFullDuplex()
		}
		p.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPCarrierThroughReverseProxy(t *testing.T) {
	t.Run("streaming, nothing buffered", func(t *testing.T) {
		f := httpMix(t)
		p := reverseProxy(t, f.addr, false)
		f.mustJoin(t, p.URL+"/ws?kinds=streaming")
		addr, _ := f.startTunnel(t, echoListener(t), "")
		visit(t, addr, 1<<20)
	})
	t.Run("split, nothing buffered", func(t *testing.T) {
		f := httpMix(t)
		p := reverseProxy(t, f.addr, false)
		f.mustJoin(t, p.URL+"/ws?kinds=split")
		addr, _ := f.startTunnel(t, echoListener(t), "")
		visit(t, addr, 1<<20)
	})
	t.Run("streaming cannot pass a buffered body", func(t *testing.T) {
		f := httpMix(t)
		p := reverseProxy(t, f.addr, true)
		if _, err := f.dialHTTP(t, p.URL+"/ws?kinds=streaming", client.NewCoreExit("", 0), 2*time.Second); err == nil {
			t.Fatal("streaming passed a buffering proxy")
		}
	})
	t.Run("a buffered body falls back to websocket", func(t *testing.T) {
		f := httpMix(t)
		p := reverseProxy(t, f.addr, true)
		s, err := f.dialHTTP(t, p.URL+"/ws?kinds=streaming,websocket", client.NewCoreExit("", 0), 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if s.Carrier() != "ws" {
			t.Fatalf("carrier %q", s.Carrier())
		}
		f.waitConnected(t)
		addr, _ := f.startTunnel(t, echoListener(t), "")
		visit(t, addr, 1<<20)
	})
}

// A wrong key on an http carrier is not told apart from a closed path.
func TestHTTPCarrierWrongKey(t *testing.T) {
	f := httpMix(t)
	cands, err := connprofile.Parse("http://"+f.addr+"/ws?kinds=streaming", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: "not-a-key", Timeout: 2 * time.Second}).Dial(ctx); err == nil {
		_ = s.Close()
		t.Fatal("an unknown key connected")
	}
	select {
	case got := <-f.fallback.got:
		if !strings.HasPrefix(got, "POST") {
			t.Fatalf("fallback got %q", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("did not reach the fallback")
	}
}
