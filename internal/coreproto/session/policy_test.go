package session

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
)

// deniedUDP5Exit refuses addressed UDP as a provider's policy does.
type deniedUDP5Exit struct {
	plainExit
	served atomic.Int32
}

func (e *deniedUDP5Exit) AdmitUDP5() error { return errors.New("refused") }
func (e *deniedUDP5Exit) ServeUDP5(context.Context, net.Conn) {
	e.served.Add(1)
}

// The refusal comes before the reply that would say the link is up.
func TestAddressedUDPRefusedBeforeAck(t *testing.T) {
	exit := &deniedUDP5Exit{}
	l := startLink(t, DialConfig{Exit: exit})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.server.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkUDP, WantReply: true})
	if err == nil {
		t.Fatal("addressed UDP acknowledged")
	}
	if exit.served.Load() != 0 {
		t.Fatal("addressed UDP served")
	}
}

// A revoked server-exit link is seen as ended, its session still up (§9.1).
func TestServerExitRevocationEndsLink(t *testing.T) {
	p := startPeer(t, true)
	p.outer.Hold(true, nil) // the session outlives the link
	v := p.visitor(t, wire.ConnAuto, time.Minute)
	ctx := peerCtx(t)
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: p.echo, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	l := v.live()
	p.a.RevokeLinks(func(int, int, int) bool { return true })
	waitUntil(t, "link ended", func() bool { return l.ended() })
	if l.exitSession.zc.Context().Err() != nil {
		t.Fatal("the session ended too")
	}
}

// A link that ended gives up its need of the outer session.
func TestNeedClearedWhenLinkEnds(t *testing.T) {
	p := startPeer(t, false)
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	ctx := peerCtx(t)
	echoOnce(t, ctx, v, "up")
	p.a.RevokeLinks(func(int, int, int) bool { return true })
	waitUntil(t, "need cleared", func() bool {
		p.outer.mu.Lock()
		defer p.outer.mu.Unlock()
		return !p.outer.need[v]
	})
}

func TestDirectActiveOnlyWhenOutboundActive(t *testing.T) {
	join := zmux.PipeStats{Carrier: zmux.CarrierQUIC, Outbound: zmux.OutboundJoining}
	if directActive([]zmux.PipeStats{join}) {
		t.Fatal("a joining pipe counted")
	}
	join.Outbound = zmux.OutboundActive
	if !directActive([]zmux.PipeStats{join}) {
		t.Fatal("an active pipe not counted")
	}
}

// A tick that saw the old path down leaves a path punched since.
func TestStepKeepsNewlyPunchedPath(t *testing.T) {
	var closedOld, closedNew atomic.Int32
	oldPath := &coreapi.DirectPath{Close: func() { closedOld.Add(1) }}
	newPath := &coreapi.DirectPath{Close: func() { closedNew.Add(1) }}
	l := &visitorLink{direct: newPath, upPath: oldPath}
	l.closeUpPath()
	if closedNew.Load() != 0 || l.direct != newPath {
		t.Fatal("the new path was closed")
	}
	l.direct = oldPath
	l.closeUpPath()
	if closedOld.Load() != 1 || l.direct != nil {
		t.Fatal("the old path stayed")
	}
}

func snappyGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "klauspost/compress/s2.(*Writer)")
}

// Paths that give up an encoder must stop its goroutine.
func TestCompressDroppedEncoderStops(t *testing.T) {
	before := snappyGoroutines()
	for range 8 {
		c := &compressConn{alg: wire.CompressSnappy}
		e, err := newEncoder(wire.CompressSnappy)
		if err != nil {
			t.Fatal(err)
		}
		e.Reset(discard{})
		_, _ = e.Write([]byte("start the writer"))
		c.enc = e
		c.dropEncoder()
		if c.enc != nil {
			t.Fatal("encoder kept")
		}
	}
	waitUntil(t, "goroutines gone", func() bool { return snappyGoroutines() <= before })
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestCompressFinishOutlastsResume(t *testing.T) {
	if compressFinishTimeout <= defaultResumeTimeout {
		t.Fatalf("finish bound %v does not cover a suspension of %v", compressFinishTimeout, defaultResumeTimeout)
	}
}
