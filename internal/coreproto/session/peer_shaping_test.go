package session

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/seal"
	"github.com/zmuxio/zmux-go/shaping"
)

func quicBundle(def, q int) string {
	b := func(n int) string { return `"budget":{"wire":{"initial":` + strconv.Itoa(n) + `}}` }
	return `{"kinds":{"default":[{` + b(def) + `}],"quic":[{` + testHandshakes + `,` + testMachines + `,` + b(q) + `}]}}`
}

// defaultOnly has no entry of the kind of a direct path.
func defaultOnly(n int) string {
	return `{"kinds":{"default":[{` + testHandshakes + `,` + testMachines + `,"budget":{"wire":{"initial":` + strconv.Itoa(n) + `}}}]}}`
}

// shapedEnds turns the switch of the given clients on, sets the bundle and
// waits until both ends hold what they are due.
func (p *peerEnv) shapedEnds(t *testing.T, ctx context.Context, bundle string, provider, visitor bool) {
	t.Helper()
	p.outer.Hold(true, nil) // CONFIG reaches a visitor only while its session is up
	p.a.ClientChanged(shapedRecord(1, "prov", provider))
	p.a.ClientChanged(shapedRecord(2, "vis", visitor))
	p.a.SetShaping([]byte(bundle))
	if _, err := p.outer.Get(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bundles", func() bool {
		return (p.provider.d.plan() != nil) == provider && (p.outer.d.plan() != nil) == visitor
	})
}

// providerSession is the nested session of the one link the provider holds.
func (p *peerEnv) providerSession(t *testing.T) *zmux.Conn {
	t.Helper()
	var zc *zmux.Conn
	waitFor(t, "provider session", func() bool {
		slot := p.provider.d.plans
		slot.mu.Lock()
		defer slot.mu.Unlock()
		for n := range slot.nested {
			zc = n.zc
		}
		return zc != nil
	})
	return zc
}

func spent(zc *zmux.Conn) uint64 { return zc.ShapingStats().WireSpent }

func openEcho(t *testing.T, ctx context.Context, v *Visitor) net.Conn {
	t.Helper()
	c, err := v.OpenLink(ctx, coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// bulkEcho sends n patterned bytes and checks that they all come back.
func bulkEcho(t *testing.T, c net.Conn, n int) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	want := make([]byte, n)
	for i := range want {
		want[i] = byte(i * 7)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(want)
		errc <- err
	}()
	got := make([]byte, n)
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("data differs")
	}
}

func TestNestedShapedOnDirect(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	p.shapedEnds(t, ctx, quicBundle(900000, 50000), true, true)
	v := p.visitor(t, wire.ConnDirect, time.Hour)
	c := openEcho(t, ctx, v)
	bulkEcho(t, c, 128<<10)
	vz, pz := v.live().zc, p.providerSession(t)
	if spent(vz) == 0 || spent(pz) == 0 {
		t.Fatalf("not shaped: visitor %d provider %d", spent(vz), spent(pz))
	}
	t.Logf("direct: visitor %+v provider %+v", vz.ShapingStats(), pz.ShapingStats())
	for what, zc := range map[string]*zmux.Conn{"visitor": vz, "provider": pz} {
		if s := zc.ShapingStats(); s.HandshakeUnplanned != 0 || s.Faults != 0 {
			t.Fatalf("%s: %+v", what, s)
		}
		if left := zc.ShapingMeters().WireLeft; left > 50000 {
			t.Fatalf("%s: left %d, not the account of the quic entry", what, left)
		}
	}
	// The outer sessions have accounts of their own, of the default entry.
	for what, zc := range map[string]*zmux.Conn{"visitor": p.outer.live().zc, "provider": p.provider.zc} {
		if left := zc.ShapingMeters().WireLeft; left <= 50000 {
			t.Fatalf("outer %s: left %d", what, left)
		}
	}
}

func TestNestedDirectFallsBackToDefault(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	p.shapedEnds(t, ctx, defaultOnly(70000), true, true)
	v := p.visitor(t, wire.ConnDirect, time.Hour)
	bulkEcho(t, openEcho(t, ctx, v), 16<<10)
	vz := v.live().zc
	if spent(vz) == 0 {
		t.Fatal("not shaped")
	}
	if left := vz.ShapingMeters().WireLeft; left == 0 || left > 70000 {
		t.Fatalf("left %d", left)
	}
}

func TestNestedRelayNotShaped(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	p.shapedEnds(t, ctx, defaultOnly(900000), true, true)
	v := p.visitor(t, wire.ConnRelay, time.Hour)
	bulkEcho(t, openEcho(t, ctx, v), 128<<10)
	for what, zc := range map[string]*zmux.Conn{"visitor": v.live().zc, "provider": p.providerSession(t)} {
		if s := zc.ShapingStats(); s != (shaping.Stats{}) {
			t.Fatalf("%s: %+v", what, s)
		}
	}
}

func TestNestedNoBundleNothingChanges(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	v := p.visitor(t, wire.ConnDirect, time.Hour)
	bulkEcho(t, openEcho(t, ctx, v), 128<<10)
	for what, zc := range map[string]*zmux.Conn{"visitor": v.live().zc, "provider": p.providerSession(t)} {
		if s := zc.ShapingStats(); s != (shaping.Stats{}) {
			t.Fatalf("%s: %+v", what, s)
		}
	}
}

// helloLen is the size of the first write of an Establish on a carrier of the
// given kind.
func helloLen(t *testing.T, kind zmux.CarrierKind, slot *planSlot) int {
	t.Helper()
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	got := make(chan int, 1)
	go func() {
		buf := make([]byte, 1<<16)
		n, _ := b.Read(buf)
		got <- n
		_ = b.Close()
	}()
	handle, psk, err := seal.DeriveHandlePSK(bytes.Repeat([]byte{1}, 32), []byte(linkDomain), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg := &seal.ClientConfig{Anchor: &anchor{}, Mode: zmux.Sealed, Handle: handle, PSK: psk}
	if slot != nil {
		cfg.Shaper = slot.handshake
	}
	_, _ = seal.Establish(ctx, zmux.PipeIO{R: a, W: a, Carrier: kind}, cfg)
	return <-got
}

func TestNestedEstablishLengths(t *testing.T) {
	plain := helloLen(t, zmux.CarrierQUIC, nil)
	slot := &planSlot{nested: map[*nestedShaping]struct{}{}}
	if n := helloLen(t, zmux.CarrierQUIC, slot); n != plain {
		t.Fatalf("no bundle: %d, plain %d", n, plain)
	}
	p, err := newShapingPlan([]byte(quicBundle(900000, 900000)))
	if err != nil {
		t.Fatal(err)
	}
	slot.keep(p)
	if n := helloLen(t, zmux.CarrierQUIC, slot); !nearly(n, 1400) {
		t.Fatalf("direct: %d", n)
	}
	if n := helloLen(t, 0, slot); n != helloLen(t, 0, nil) {
		t.Fatalf("relay: %d", n)
	}
	t.Logf("hello: plain %d, shaped direct about 1400", plain)
}

func TestNestedFollowsReplaceAndClear(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	p.outer.Hold(true, nil)
	v := p.visitor(t, wire.ConnDirect, time.Hour)
	if _, err := p.outer.Get(ctx); err != nil {
		t.Fatal(err)
	}
	c := openEcho(t, ctx, v)
	bulkEcho(t, c, 16<<10)
	vz, pz := v.live().zc, p.providerSession(t)
	if spent(vz) != 0 || spent(pz) != 0 {
		t.Fatal("shaped before a bundle")
	}

	// A bundle arriving reaches the session that is up.
	p.shapedEnds(t, ctx, quicBundle(900000, 600000), true, true)
	waitFor(t, "first bundle followed", func() bool {
		return vz.ShapingMeters().WireLeft > 0 && pz.ShapingMeters().WireLeft > 0
	})
	bulkEcho(t, c, 64<<10)
	if spent(vz) == 0 || spent(pz) == 0 {
		t.Fatalf("not shaped: %d %d", spent(vz), spent(pz))
	}

	next := quicBundle(900000, 30000)
	p.a.SetShaping([]byte(next))
	waitFor(t, "replacement", func() bool {
		return vz.ShapingMeters().WireLeft <= 30000 && pz.ShapingMeters().WireLeft <= 30000
	})

	p.a.SetShaping(nil)
	waitFor(t, "clearing", func() bool { return p.provider.d.plan() == nil && p.outer.d.plan() == nil })
	for _, slot := range []*planSlot{p.provider.d.plans, p.outer.d.plans} {
		slot.mu.Lock() // the change is complete once keep has let go
		slot.mu.Unlock()
	}
	before := [2]uint64{spent(vz), spent(pz)}
	bulkEcho(t, c, 64<<10)
	if now := [2]uint64{spent(vz), spent(pz)}; now != before {
		t.Fatalf("shaped after clearing: %v -> %v", before, now)
	}
}

// Shaping is a sender's matter: one end with a bundle and one without work
// together, in either arrangement.
func TestNestedOneEndShaped(t *testing.T) {
	for _, visitor := range []bool{true, false} {
		t.Run("visitor="+strconv.FormatBool(visitor), func(t *testing.T) {
			p := startPeer(t, false)
			ctx := peerCtx(t)
			p.shapedEnds(t, ctx, quicBundle(900000, 900000), !visitor, visitor)
			v := p.visitor(t, wire.ConnDirect, time.Hour)
			bulkEcho(t, openEcho(t, ctx, v), 256<<10)
			shaped, plain := v.live().zc, p.providerSession(t)
			if !visitor {
				shaped, plain = plain, shaped
			}
			if spent(shaped) == 0 {
				t.Fatal("the end with a bundle is not shaped")
			}
			if spent(plain) != 0 {
				t.Fatalf("the end without one spent %d", spent(plain))
			}
		})
	}
}

func TestNestedAutoShapedOnceDirect(t *testing.T) {
	p := startPeer(t, false)
	ctx := peerCtx(t)
	p.shapedEnds(t, ctx, defaultOnly(900000), true, true)
	p.punch.fail.Store(true)
	v := p.visitor(t, wire.ConnAuto, time.Hour)
	c := openEcho(t, ctx, v)
	bulkEcho(t, c, 64<<10)
	vz, pz := v.live().zc, p.providerSession(t)
	if spent(vz) != 0 || spent(pz) != 0 {
		t.Fatalf("relay shaped: %d %d", spent(vz), spent(pz))
	}
	p.punch.fail.Store(false)
	v.live().mu.Lock()
	v.live().nextTry = time.Time{}
	v.live().mu.Unlock()
	waitPipes(t, v, "direct", func(d, _ bool) bool { return d })
	waitUntil(t, "traffic on the direct path shaped", func() bool {
		bulkEcho(t, c, 16<<10)
		return spent(vz) > 0 && spent(pz) > 0
	})
	t.Logf("auto after direct: visitor %d provider %d", spent(vz), spent(pz))
}
