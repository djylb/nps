package session

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/model"
	"github.com/zmuxio/zmux-go"
)

func TestUsageWire(t *testing.T) {
	in := []wire.LinkUsage{{LinkID: 7, In: 1, Out: 2}, {LinkID: 1 << 61, In: 1 << 40, Out: 0}}
	got, err := wire.ParseUsage(wire.AppendUsage(nil, in))
	if err != nil || len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err = wire.ParseUsage(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty body: %+v %v", got, err)
	}
	for name, body := range map[string][]byte{
		"zero link": wire.AppendUsage(nil, []wire.LinkUsage{{LinkID: 0, In: 1}}),
		"truncated": wire.AppendUsage(nil, in)[:5],
		"no entry":  {0x04, 0x00}, // field 1, W=0: not an entry
	} {
		if _, err := wire.ParseUsage(body); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if op, _, err := wire.ParseOpenOp(wire.AppendUsageOpen()); err != nil || op != wire.OpP2PUsage || !op.FromClient() || op.FromServer() {
		t.Fatalf("open %v %v", op, err)
	}
	if wire.OpConfigUpload == wire.OpP2PUsage || wire.OpRegister == wire.OpP2PUsage || !wire.OpConfigUpload.Known() || wire.Op(12).Known() {
		t.Fatal("op table")
	}
}

func TestDirectMeter(t *testing.T) {
	direct := func(id, in, out uint64) zmux.PipeStats {
		return zmux.PipeStats{Pipe: id, Carrier: zmux.CarrierQUIC, BytesReceived: in, BytesSent: out}
	}
	relay := zmux.PipeStats{Pipe: 9, BytesReceived: 1000, BytesSent: 1000}
	var m directMeter
	check := func(in, out uint64) {
		t.Helper()
		if i, o := m.total(); i != in || o != out {
			t.Fatalf("total %d/%d, want %d/%d", i, o, in, out)
		}
	}
	m.sample([]zmux.PipeStats{relay})
	check(0, 0) // a relay PIPE does not count
	m.sample([]zmux.PipeStats{relay, direct(1, 10, 20)})
	check(10, 20)
	m.sample([]zmux.PipeStats{direct(1, 15, 20), direct(2, 1, 2)})
	check(16, 22)
	m.sample([]zmux.PipeStats{direct(2, 5, 6)}) // pipe 1 left: it stays counted
	check(20, 26)
	m.sample(nil) // the session closed
	check(20, 26)
	m.sample([]zmux.PipeStats{direct(3, 1, 1)})
	check(21, 27)
}

// sendUsage reports on a stream of s as a provider does.
func sendUsage(t *testing.T, s *clientSession, list ...wire.LinkUsage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPreamble(wire.AppendUsageOpen())
	if _, err := st.Write(wire.AppendUsage(nil, list)); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
}

// totals reads the counted direct bytes after the server handled what was sent.
func (p *peerEnv) totals() (in, out uint64) { return p.directIn.Load(), p.directOut.Load() }

func (p *peerEnv) providerLink(t *testing.T) *providerLink {
	t.Helper()
	var l *providerLink
	waitUntil(t, "provider link", func() bool {
		p.provider.mu.Lock()
		defer p.provider.mu.Unlock()
		for _, x := range p.provider.provided {
			l = x
		}
		return l != nil
	})
	return l
}

func echoBytes(t *testing.T, v *Visitor, n int) {
	t.Helper()
	c, err := v.OpenLink(peerCtx(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, ProviderTarget: true, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	msg := bytes.Repeat([]byte("d"), n)
	go func() { _, _ = c.Write(msg) }()
	if _, err := io.ReadFull(c, make([]byte, n)); err != nil {
		t.Fatal(err)
	}
}

// Direct traffic is reported by the provider on its interval and counted by
// the server; relayed traffic is not.
func TestUsageDirectCounted(t *testing.T) {
	const size = 300 << 10
	p := startPeerUsage(t, false, 0, 100*time.Millisecond)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	echoBytes(t, v, size)
	waitUntil(t, "usage reported", func() bool {
		in, out := p.totals()
		return in >= size && out >= size
	})
	in, out := p.totals()
	l := p.providerLink(t)
	mi, mo := l.meter.total()
	if in > mi+(64<<10) || out > mo+(64<<10) || mi < size || mo < size {
		t.Fatalf("server counted %d/%d, provider metered %d/%d", in, out, mi, mo)
	}
	// nothing changes, nothing is reported: the next flush has nothing to send
	waitUntil(t, "quiet", func() bool { return !l.flushUsage() })
	if l.flushUsage() {
		t.Fatal("an unchanged total was reported")
	}
}

func TestUsageRelayNotCounted(t *testing.T) {
	p := startPeerUsage(t, false, 0, 50*time.Millisecond)
	v := p.visitor(t, wire.ConnRelay, time.Minute)
	echoBytes(t, v, 100<<10)
	time.Sleep(400 * time.Millisecond)
	l := p.providerLink(t)
	if mi, mo := l.meter.total(); mi != 0 || mo != 0 {
		t.Fatalf("relay metered %d/%d", mi, mo)
	}
	if in, out := p.totals(); in != 0 || out != 0 {
		t.Fatalf("relay counted %d/%d", in, out)
	}
}

// Reports are cumulative: a repeat counts nothing, a lower value is no
// decrease, and an increase counts only the difference.
func TestUsageCumulative(t *testing.T) {
	p := startPeerUsage(t, false, 0, time.Hour)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	want := func(in, out uint64) {
		t.Helper()
		waitUntil(t, "usage handled", func() bool { i, o := p.totals(); return i == in && o == out })
		time.Sleep(100 * time.Millisecond)
		if i, o := p.totals(); i != in || o != out {
			t.Fatalf("totals %d/%d, want %d/%d", i, o, in, out)
		}
	}
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 100, Out: 50})
	want(100, 50)
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 100, Out: 50}) // a retransmission
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 90, Out: 10})  // late and older
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 130, Out: 50})
	want(130, 50)
	// the late report did not lower the last value
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 130, Out: 80})
	want(130, 80)
	// two entries of one stream, one of them unknown
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID + 1, In: 5, Out: 5}, wire.LinkUsage{LinkID: r.LinkID, In: 131, Out: 80})
	want(131, 80)
}

// Only the provider of a link can report it; unknown links are ignored.
func TestUsageForgedLinkIgnored(t *testing.T) {
	p := startPeerUsage(t, false, 0, time.Hour)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	other := p.dial(NewDialer(DialConfig{Candidates: p.cands(p.addr), VKey: "other", Agent: "t", Timeout: 3 * time.Second}))
	sendUsage(t, other, wire.LinkUsage{LinkID: r.LinkID, In: 999, Out: 999})
	sendUsage(t, vis, wire.LinkUsage{LinkID: r.LinkID, In: 999, Out: 999}) // the visitor does not report
	time.Sleep(300 * time.Millisecond)
	if in, out := p.totals(); in != 0 || out != 0 {
		t.Fatalf("a forged report counted %d/%d", in, out)
	}
	// the forgery did not move the last value either
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 10, Out: 20})
	waitUntil(t, "real report", func() bool { i, o := p.totals(); return i == 10 && o == 20 })
}

// A report arrives after the link ended, for five minutes; a reconnecting
// provider sends the same cumulative value and nothing is counted twice.
func TestUsageAfterLinkEnd(t *testing.T) {
	p := startPeerUsage(t, false, 0, time.Hour)
	now := time.Now()
	p.a.usageMu.Lock()
	p.a.clock = func() time.Time { return now }
	p.a.usageMu.Unlock()
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 10, Out: 10})
	waitUntil(t, "first", func() bool { i, _ := p.totals(); return i == 10 })

	p.a.RevokeLinks(func(tunnel, _, _ int) bool { return tunnel == 7 })
	waitUntil(t, "link ended", func() bool { return p.a.linkOf(r.LinkID) == nil })
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 10, Out: 10}) // repeated after a reconnect
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 25, Out: 12})
	waitUntil(t, "late report", func() bool { i, o := p.totals(); return i == 25 && o == 12 })

	p.a.usageMu.Lock()
	now = now.Add(usageKeep + time.Second)
	p.a.usageMu.Unlock()
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 99, Out: 99})
	time.Sleep(300 * time.Millisecond)
	if in, out := p.totals(); in != 25 || out != 12 {
		t.Fatalf("a report past the keep time counted: %d/%d", in, out)
	}
	p.a.endUsage(0) // sweeps the expired records
	p.a.usageMu.Lock()
	n := len(p.a.usage)
	p.a.usageMu.Unlock()
	if n != 0 {
		t.Fatalf("%d records kept", n)
	}
}

// With an interval that never comes, the link's end still reports what it
// carried.
func TestUsageFinalReport(t *testing.T) {
	const size = 200 << 10
	p := startPeerUsage(t, false, 0, time.Hour)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	echoBytes(t, v, size)
	if in, out := p.totals(); in != 0 || out != 0 {
		t.Fatalf("reported before the end: %d/%d", in, out)
	}
	p.a.RevokeLinks(func(tunnel, _, _ int) bool { return tunnel == 7 })
	waitUntil(t, "final report", func() bool {
		in, out := p.totals()
		return in >= size && out >= size
	})
}

// A negative interval reports nothing.
func TestUsageOff(t *testing.T) {
	p := startPeerUsage(t, false, 0, -1)
	v := p.visitor(t, wire.ConnDirect, time.Minute)
	echoBytes(t, v, 50<<10)
	p.a.RevokeLinks(func(tunnel, _, _ int) bool { return tunnel == 7 })
	time.Sleep(500 * time.Millisecond)
	if in, out := p.totals(); in != 0 || out != 0 {
		t.Fatalf("counted %d/%d", in, out)
	}
}

// Clearing the statistics zeroes the tunnel's count, while the server keeps
// each link's last value: only what is new after the clear is counted.
func TestUsageClearCountsOnlyNew(t *testing.T) {
	p := startPeerUsage(t, false, 0, time.Hour)
	var flow model.Flow
	add := func(in, out uint64) { flow.AddDirect(int64(in), int64(out)) }
	p.onDirect.Store(&add)
	ctx := peerCtx(t)
	vis, err := p.outer.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := linkReply(t, ctx, vis)
	counted := func(in, out int64) {
		t.Helper()
		waitUntil(t, "counted", func() bool { v := flow.Values(); return v.DirectIn == in && v.DirectOut == out })
	}
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 100, Out: 40})
	counted(100, 40)
	flow.ResetTraffic()
	if v := flow.Values(); v.DirectIn != 0 || v.DirectOut != 0 {
		t.Fatalf("not cleared: %+v", v)
	}
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 100, Out: 40}) // the same cumulative value
	sendUsage(t, p.provider, wire.LinkUsage{LinkID: r.LinkID, In: 130, Out: 45})
	counted(30, 5)
	if v := flow.Values(); v.InletFlow != 0 || v.ExportFlow != 0 || v.Used != 0 {
		t.Fatalf("direct traffic counted as relayed: %+v", v)
	}
}
