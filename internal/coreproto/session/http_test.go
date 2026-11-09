package session

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/connprofile"
)

// httpServer serves the HTTP carriers of e; everything else is 404. TLS, when
// set, is served with HTTP/2 enabled.
func (e *env) httpServer(t *testing.T, https bool) *httptest.Server {
	t.Helper()
	h, err := e.a.HTTPCarriers(http.NotFoundHandler(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	if https {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return srv
}

// A cut carrier is replaced under a streaming http kind, and every byte
// still arrives.
func TestHTTPCarrierResume(t *testing.T) {
	for _, kind := range []string{"streaming", "split"} {
		t.Run(kind, func(t *testing.T) {
			e := startOpts(t, "tcp", Options{ResumeTimeout: 10 * time.Second}, rec(1, "key", true))
			srv := e.httpServer(t, false)
			p := newCutProxy(t, srv.Listener.Addr().String())
			cs := e.dial(e.resumeDialer("http://"+p.addr()+"/x?kinds="+kind, 10*time.Second, ""))
			s := e.waitOnline()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			c, err := s.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: echoServer(t), WantReply: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(20 * time.Second))
			during := bytes.Repeat([]byte("during-cut."), 2000)
			p.down()
			suspendedSoon(t, s)
			if _, err := c.Write(during); err != nil {
				t.Fatal(err)
			}
			e.noOffline()
			p.block(false)
			got := make([]byte, len(during))
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, during) {
				t.Fatalf("after resume: %v", err)
			}
			waitUntil(t, "resume", func() bool { return !s.Suspended() })
			select {
			case <-cs.Done():
				t.Fatalf("npc session ended: %v", cs.Err())
			default:
			}
		})
	}
}

// A connection that ends after the request went out and before any response
// fails the carrier at once: the join waiting for the reply must not sit out
// its deadline.
func TestHTTPStreamingFailsWithConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	hello := []byte("hello")
	wg.Go(func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		// Close only once the first body bytes arrived: the transport is
		// then blocked on the next read of the body.
		var seen []byte
		buf := make([]byte, 4096)
		for !bytes.Contains(seen, hello) {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			seen = append(seen, buf[:n]...)
		}
	})
	cands, err := connprofile.Parse("http://"+ln.Addr().String()+"/x?kinds=streaming", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	d := NewDialer(DialConfig{Candidates: cands, VKey: "key", Agent: "t", Timeout: 3 * time.Second, Exit: plainExit{}, Key: newKey(t)})
	ca, err := d.dialHTTP(context.Background(), connprofile.Attempts(cands)[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ca.pipe.W.Close(); _ = ca.pipe.R.Close() }()
	if _, err := ca.pipe.W.Write(hello); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { _, err := ca.pipe.R.Read(make([]byte, 16)); read <- err }()
	select {
	case err := <-read:
		if err == nil {
			t.Fatal("read succeeded on a connection without a response")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("carrier did not fail with its connection")
	}
}

// A multi-pipe session uses a tls member and an https member; https over a
// real HTTP/2 server.
func TestHTTPCarrierMultiPipe(t *testing.T) {
	e := start(t, "tls", rec(1, "key", true))
	srv := e.httpServer(t, true)
	server := "tls://" + e.addr + "?count=2|" + srv.URL + "/x?kinds=streaming"
	cands, err := connprofile.Parse(server, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	cs := e.dial(NewDialer(DialConfig{Candidates: cands, VKey: "key", Agent: "t", Timeout: 3 * time.Second, Pipes: connprofile.PipesMulti, Exit: plainExit{}, Key: newKey(t)}))
	s := e.waitOnline()
	waitFor(t, "CONFIG", func() bool { return cs.appliedSeq() == 1 })
	waitFor(t, "three pipes", func() bool { return len(cs.zc.Pipes()) == 3 })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := s.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: echoServer(t), WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	data := bytes.Repeat([]byte("multi-pipe."), 40000)
	werr := make(chan error, 1)
	go func() { _, err := c.Write(data); werr <- err }()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("echo: %v", err)
	}
	if err := <-werr; err != nil {
		t.Fatal(err)
	}
}

// A multi-pipe session that starts on an http candidate keeps count pipes:
// the first is retired once the pool's stands.
func TestHTTPCarrierMultiRetiresFirst(t *testing.T) {
	e := start(t, "tcp", rec(1, "key", true))
	srv := e.httpServer(t, false)
	cands, err := connprofile.Parse(srv.URL+"/x?kinds=streaming", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	cs := e.dial(NewDialer(DialConfig{Candidates: cands, VKey: "key", Agent: "t", Timeout: 3 * time.Second, Pipes: connprofile.PipesMulti, Exit: plainExit{}, Key: newKey(t)}))
	e.waitOnline()
	first := cs.zc.Pipes()[0]
	waitFor(t, "first pipe retired", func() bool {
		p := cs.zc.Pipes()
		return len(p) == 1 && p[0] != first
	})
}
