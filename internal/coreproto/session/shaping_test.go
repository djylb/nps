package session

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/shaping"
)

const testBundle = `{"kinds":{"default":[{}]}}`

const (
	testHandshakes = `"handshakes":{"establish":{"hello":{"pad_to":1400},"reply":{"pad_to":1450},"finish":{"pad_to":1500}},` +
		`"join":{"hello":{"pad_to":700},"hello_early":{"pad_to":700},"reply":{"pad_to":750},"confirm":{"pad_to":800}}}`
	testMachines = `"machines":[{"start":0,"states":[{"record":{"pad":37}}]}]`
)

func budgetBundle(def, ws int) string {
	b := func(n int) string { return `"budget":{"wire":{"initial":` + strconv.Itoa(n) + `}}` }
	return `{"kinds":{"default":[{` + testHandshakes + `,` + testMachines + `,` + b(def) + `}],` +
		`"websocket":[{` + testMachines + `,` + b(ws) + `}]}}`
}

// wireLog records the sizes of the first writes and reads of each carrier.
type wireLog struct {
	mu    sync.Mutex
	conns []*loggedConn
}

type loggedConn struct {
	net.Conn
	l             *wireLog
	writes, reads []int
}

func (c *loggedConn) Write(b []byte) (int, error) {
	c.l.mu.Lock()
	c.writes = append(c.writes, len(b))
	c.l.mu.Unlock()
	return c.Conn.Write(b)
}

func (c *loggedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.l.mu.Lock()
	c.reads = append(c.reads, n)
	c.l.mu.Unlock()
	return n, err
}

func (l *wireLog) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	lc := &loggedConn{Conn: c, l: l}
	l.mu.Lock()
	l.conns = append(l.conns, lc)
	l.mu.Unlock()
	return lc, nil
}

// firsts is the size of the first write and of the first read of carrier i.
func (l *wireLog) firsts(t *testing.T, i int) (int, int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if i >= len(l.conns) || len(l.conns[i].writes) == 0 || len(l.conns[i].reads) == 0 {
		t.Fatalf("carrier %d: nothing seen", i)
	}
	return l.conns[i].writes[0], l.conns[i].reads[0]
}

func loggedDialer(e *env, vkey string, multi bool) (*Dialer, *wireLog) {
	l := &wireLog{}
	cfg := DialConfig{Candidates: e.cands(e.addr), VKey: vkey, Agent: "t", Timeout: 3 * time.Second, Dial: l.dial}
	if multi {
		cfg.Pipes = connprofile.PipesMulti
	}
	d := NewDialer(cfg)
	d.keepPlan(nil)
	return d, l
}

func shapedRecord(id int, vkey string, on bool) coreapi.ClientRecord {
	return coreapi.ClientRecord{ID: id, VKey: vkey, Enabled: true, Crypt: true, Shaping: on}
}

func nearly(n, want int) bool { return n >= want && n <= want+8 }

func ping(t *testing.T, zc *zmux.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := zc.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func clientPlan(cs *clientSession) *shapingPlan {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.plan
}

func TestShapingOnlyWhenSwitchAndSource(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sw, source, want bool
	}{{"both", true, true, true}, {"switch off", false, true, false}, {"no source", true, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := start(t, "tcp", shapedRecord(1, "key-1", tc.sw))
			if tc.source {
				e.a.SetShaping([]byte(testBundle))
			}
			cs := e.dial(e.dialer("key-1"))
			e.waitOnline()
			waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
			if have := clientPlan(cs) != nil; have != tc.want {
				t.Fatalf("shaping present=%v, want %v", have, tc.want)
			}
		})
	}
}

func TestShapingNeedsCapability(t *testing.T) {
	e := start(t, "tcp", shapedRecord(1, "key-1", true))
	e.a.SetShaping([]byte(testBundle))
	if e.a.shapingFor(&ticket{}, true) != nil {
		t.Fatal("a client without the capability is shaped")
	}
	withCap := &ticket{hello: wire.Hello{Caps: []uint64{wire.CapShaping}}}
	if e.a.shapingFor(withCap, true) == nil || e.a.shapingFor(withCap, false) != nil {
		t.Fatal("the switch and the bundle do not decide")
	}
}

// The first connection has no bundle; the next starts with it, in both
// directions and on the handshake messages.
func TestShapingBothDirectionsAndHandshake(t *testing.T) {
	e := start(t, "tcp", shapedRecord(1, "key-1", true))
	e.a.SetShaping([]byte(budgetBundle(100000, 200000)))
	d, l := loggedDialer(e, "key-1", false)

	cs := e.dial(d)
	s := e.waitOnline()
	waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
	hello0, reply0 := l.firsts(t, 0)
	if nearly(hello0, 1400) {
		t.Fatalf("first connection: hello of %d is shaped", hello0)
	}
	if !nearly(reply0, 1450) {
		t.Fatalf("reply %d, want about 1450", reply0)
	}
	if clientPlan(cs) == nil || d.plan() == nil {
		t.Fatal("bundle not kept")
	}
	ping(t, cs.zc)
	ping(t, s.zc)
	cst, sst := cs.zc.ShapingStats(), s.zc.ShapingStats()
	if cst.WireSpent == 0 || sst.WireSpent == 0 {
		t.Fatalf("records not padded: client %+v, server %+v", cst, sst)
	}
	t.Logf("client %+v meters %+v", cst, cs.zc.ShapingMeters())
	t.Logf("server %+v meters %+v", sst, s.zc.ShapingMeters())
	_ = cs.Close()
	<-cs.Done()
	select {
	case <-e.offline:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end")
	}

	cs2 := e.dial(d)
	s2 := e.waitOnline()
	hello1, reply1 := l.firsts(t, 1)
	if !nearly(hello1, 1400) || !nearly(reply1, 1450) {
		t.Fatalf("reconnect: hello %d, reply %d", hello1, reply1)
	}
	t.Logf("hello %d -> %d, reply %d", hello0, hello1, reply1)
	if clientPlan(cs2) != d.plan() {
		t.Fatal("the session does not use the kept bundle")
	}
	waitFor(t, "first CONFIG", func() bool { return cs2.appliedSeq() >= 1 })
	if clientPlan(cs2) != d.plan() {
		t.Fatal("the same bundle was installed again")
	}
	if cs2.zc.ShapingMeters().WireSpent == 0 || s2.zc.ShapingMeters().WireSpent == 0 {
		t.Fatal("not shaped from the start")
	}
}

func TestShapingJoinMessages(t *testing.T) {
	e := start(t, "tcp", shapedRecord(1, "key-1", true))
	e.a.SetShaping([]byte(budgetBundle(100000, 200000)))
	d, l := loggedDialer(e, "key-1", true)
	cs := e.dial(d)
	e.waitOnline()
	waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cs.joinOne(ctx, e.cands(e.addr)[0], zmux.PipeOptions{}); err != nil {
		t.Fatal(err)
	}
	hello, reply := l.firsts(t, 1)
	if !nearly(hello, 700) || reply <= 89 || reply > 750 {
		t.Fatalf("join: hello %d, reply %d", hello, reply)
	}
	t.Logf("join hello %d, reply %d", hello, reply)
	if st := cs.zc.ShapingStats(); st.HandshakeUnplanned != 0 {
		t.Fatalf("unplanned messages: %+v", st)
	}
}

// Without a switch or a bundle nothing is padded, the handshakes and the
// join are the same length both ways, and nothing counts as unplanned.
func TestShapingOffChangesNothing(t *testing.T) {
	measure := func(t *testing.T, on, source bool) [4]int {
		e := start(t, "tcp", shapedRecord(1, "key-1", on))
		if source {
			e.a.SetShaping([]byte(budgetBundle(100000, 200000)))
		}
		d, l := loggedDialer(e, "key-1", true)
		cs := e.dial(d)
		s := e.waitOnline()
		waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := cs.joinOne(ctx, e.cands(e.addr)[0], zmux.PipeOptions{}); err != nil {
			t.Fatal(err)
		}
		ping(t, cs.zc)
		ping(t, s.zc)
		for _, st := range []shaping.Stats{cs.zc.ShapingStats(), s.zc.ShapingStats()} {
			if st != (shaping.Stats{}) {
				t.Fatalf("stats %+v", st)
			}
		}
		if clientPlan(cs) != nil || d.plan() != nil {
			t.Fatal("a bundle was kept")
		}
		h0, r0 := l.firsts(t, 0)
		h1, r1 := l.firsts(t, 1)
		return [4]int{h0, r0, h1, r1}
	}
	off, none := measure(t, false, true), measure(t, true, false)
	if off != none {
		t.Fatalf("switch off %v, no bundle %v", off, none)
	}
	t.Logf("hello, reply, join hello, join reply: %v", off)
}

func TestShapingBudgetFollowsBootCarrier(t *testing.T) {
	p, err := newShapingPlan([]byte(budgetBundle(100000, 200000)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind zmux.CarrierKind
		want uint64
	}{{0, 100000}, {zmux.CarrierWebSocket, 200000}, {zmux.CarrierQUIC, 100000}} {
		_, b := p.sessionShaper(tc.kind)
		if got := b().Wire.Initial; got != tc.want {
			t.Fatalf("kind %d: budget %d, want %d", tc.kind, got, tc.want)
		}
	}
	if f, b := (*shapingPlan)(nil).sessionShaper(0); f != nil || b != nil {
		t.Fatal("no bundle: shaper given")
	}
}

func TestShapingBudgetOnRealCarriers(t *testing.T) {
	e := start(t, "tcp", shapedRecord(1, "key-1", true))
	e.a.SetShaping([]byte(budgetBundle(100000, 200000)))
	check := func(name string, cs *clientSession, want uint64) {
		t.Helper()
		s := e.waitOnline()
		waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })
		for what, m := range map[string]shaping.Meters{"client": cs.zc.ShapingMeters(), "server": s.zc.ShapingMeters()} {
			if m.WireLeft > want || m.WireLeft < want-want/10 {
				t.Fatalf("%s %s: left %d of %d", name, what, m.WireLeft, want)
			}
			t.Logf("%s %s: left %d of %d", name, what, m.WireLeft, want)
		}
		_ = cs.Close()
		<-cs.Done()
		select {
		case <-e.offline:
		case <-time.After(3 * time.Second):
			t.Fatal("session did not end")
		}
	}
	check("tcp", e.dial(e.dialer("key-1")), 100000)

	h, err := e.a.WebSocketAnyProtocol(http.NotFoundHandler(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cands, err := connprofile.Parse("http://"+srv.Listener.Addr().String()+"/x", "ws")
	if err != nil {
		t.Fatal(err)
	}
	check("ws", e.dial(NewDialer(DialConfig{Candidates: cands, VKey: "key-1", Timeout: 3 * time.Second})), 200000)
}

func TestShapingReplaceAndClear(t *testing.T) {
	e := start(t, "tcp", shapedRecord(1, "key-1", true))
	first := budgetBundle(100000, 200000)
	e.a.SetShaping([]byte(first))
	d, _ := loggedDialer(e, "key-1", false)
	cs := e.dial(d)
	s := e.waitOnline()
	waitFor(t, "first CONFIG", func() bool { return cs.appliedSeq() >= 1 })

	next := budgetBundle(50000, 60000)
	e.a.SetShaping([]byte(next))
	waitFor(t, "replacement", func() bool {
		p := clientPlan(cs)
		return p != nil && string(p.raw) == next && s.t.plan.Load() == e.a.shaping.Load()
	})
	for what, zc := range map[string]*zmux.Conn{"client": cs.zc, "server": s.zc} {
		if m := zc.ShapingMeters(); m.WireLeft > 50000 {
			t.Fatalf("%s: left %d after the replacement", what, m.WireLeft)
		}
	}
	if string(d.plan().raw) != next {
		t.Fatal("the kept bundle was not replaced")
	}

	// A bundle that does not parse changes nothing.
	e.a.SetShaping([]byte("{"))
	if p := e.a.shaping.Load(); p == nil || string(p.raw) != next {
		t.Fatal("an invalid bundle replaced the bundle")
	}

	e.a.SetShaping(nil)
	waitFor(t, "clearing", func() bool { return clientPlan(cs) == nil && s.t.plan.Load() == nil })
	if d.plan() != nil {
		t.Fatal("the kept bundle was not cleared")
	}
	spent := [2]uint64{cs.zc.ShapingMeters().WireSpent, s.zc.ShapingMeters().WireSpent}
	ping(t, cs.zc)
	ping(t, s.zc)
	if now := [2]uint64{cs.zc.ShapingMeters().WireSpent, s.zc.ShapingMeters().WireSpent}; now != spent {
		t.Fatalf("still shaped after clearing: %v -> %v", spent, now)
	}

	// The switch of the client decides too.
	e.a.SetShaping([]byte(first))
	waitFor(t, "bundle back", func() bool { return clientPlan(cs) != nil && s.t.plan.Load() != nil })
	e.a.ClientChanged(shapedRecord(1, "key-1", false))
	waitFor(t, "switch off", func() bool { return clientPlan(cs) == nil && s.t.plan.Load() == nil })
}
