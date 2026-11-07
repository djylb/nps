package client

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

// healthTarget is a listener that can go away and come back on its address.
type healthTarget struct {
	t    *testing.T
	addr string
	ln   net.Listener
}

func newHealthTarget(t *testing.T) *healthTarget {
	ht := &healthTarget{t: t}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ht.addr = ln.Addr().String()
	ht.serve(ln)
	t.Cleanup(ht.down)
	return ht
}

func (h *healthTarget) serve(ln net.Listener) {
	h.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
}

func (h *healthTarget) down() {
	if h.ln != nil {
		_ = h.ln.Close()
		h.ln = nil
	}
}

func (h *healthTarget) up() {
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		h.t.Fatal(err)
	}
	h.serve(ln)
}

// rotation returns the addresses target hands out.
func rotation(target *model.Target) []string {
	seen := map[string]bool{}
	for range 8 {
		_ = target.Dial(func(addr string) (bool, error) {
			seen[addr] = true
			return false, nil
		})
	}
	out := make([]string, 0, len(seen))
	for addr := range seen {
		out = append(out, addr)
	}
	slices.Sort(out)
	return out
}

func sorted(addrs ...string) []string {
	slices.Sort(addrs)
	return addrs
}

func healthCheck() model.HealthCheck {
	return model.HealthCheck{Enabled: true, Type: "tcp", Interval: 1, Timeout: 1, MaxFail: 1}
}

// groupKeys returns the addresses a group of hc holds.
func groupKeys(hc *HealthChecker, group string) []string {
	var out []string
	for _, k := range hc.checker.GroupKeys(group) {
		out = append(out, k.addr)
	}
	slices.Sort(out)
	return out
}

// TestServerGivenHealthGroups runs a real bridge and npc: groups arrive, an edit
// sends its group again, equal checks merge and are released with their last
// group, and an offline target leaves only its own entry's rotation and comes
// back.
func TestServerGivenHealthGroups(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)

	const key = "healthgroupvkey0"
	client := model.NewClient(key, true, false)
	if err := db.NewClient(client); err != nil {
		t.Fatal(err)
	}
	a, b, c := newHealthTarget(t), newHealthTarget(t), newHealthTarget(t)
	list := func(ts ...*healthTarget) string {
		s := ""
		for _, x := range ts {
			s += x.addr + "\n"
		}
		return s
	}

	tunnel := &model.Tunnel{Id: 1, Mode: "tcp", Status: true, Client: client, Target: &model.Target{TargetStr: list(a, b)}}
	tunnel.SetHealthCheck(healthCheck())
	// Holds b as well but has no checks: reports of tunnel 1 must not touch it.
	bystander := &model.Tunnel{Id: 2, Mode: "tcp", Status: true, Client: client, Target: &model.Target{TargetStr: list(a, b)}}
	// One target only: nothing to fail over to, so no group.
	single := &model.Tunnel{Id: 3, Mode: "tcp", Status: true, Client: client, Target: &model.Target{TargetStr: list(a)}}
	single.SetHealthCheck(healthCheck())
	for _, tn := range []*model.Tunnel{tunnel, bystander, single} {
		if err := db.NewTask(tn); err != nil {
			t.Fatal(err)
		}
	}

	br := bridge.NewTunnel(db, &sync.Map{}, bridge.Options{DisconnectTime: 30})
	db.OnEntriesChanged(br.EntriesChanged)
	bridgeAddr := serveTCP(t, func(cn net.Conn) { br.CliProcess(conn.NewConn(cn), "tcp") })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	start := func() (*TRPClient, func()) {
		rp := NewRPClient(bridgeAddr, key, "tcp", "", "", "", nil, 30, nil)
		done := make(chan struct{})
		go func() {
			defer close(done)
			rp.Start(ctx)
		}()
		return rp, func() {
			rp.Close()
			<-done
		}
	}
	rp, stop := start()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stop()
		}
		// The bridge reads the store until the signal connection has ended.
		waitFor(t, 10*time.Second, "signal connection ended", func() bool {
			v, ok := br.Client.Load(client.Id)
			if !ok {
				return true
			}
			n, ok := v.(*bridge.Client).GetNodeByUUID(rp.uuid)
			return !ok || n.GetSignal() == nil
		})
	})
	hc := func() *HealthChecker {
		var h *HealthChecker
		waitFor(t, 10*time.Second, "npc health checker", func() bool {
			h = rp.healthChecker.Load()
			return h != nil
		})
		return h
	}
	wantKeys := func(what, group string, want []string) {
		t.Helper()
		waitFor(t, 10*time.Second, what, func() bool { return slices.Equal(groupKeys(hc(), group), want) })
	}

	// Pushed on joining; the single-target entry and the disabled one get nothing.
	wantKeys("group of tunnel 1", "t1", sorted(a.addr, b.addr))
	if got := groupKeys(hc(), "t3"); len(got) != 0 {
		t.Fatalf("single target checked: %v", got)
	}
	if got := groupKeys(hc(), "t2"); len(got) != 0 {
		t.Fatalf("disabled entry checked: %v", got)
	}

	// An offline target leaves the rotation of its entry only, and comes back.
	b.down()
	waitFor(t, 10*time.Second, "b out of rotation", func() bool { return slices.Equal(rotation(tunnel.Target), []string{a.addr}) })
	if got := rotation(bystander.Target); !slices.Equal(got, sorted(a.addr, b.addr)) {
		t.Fatalf("report reached another entry: %v", got)
	}
	b.up()
	waitFor(t, 10*time.Second, "b back in rotation", func() bool { return slices.Equal(rotation(tunnel.Target), sorted(a.addr, b.addr)) })

	// Real traffic keeps a target from being probed; an idle one is probed.
	key1 := healthKey{spec: healthSpec{typ: "tcp", interval: 1, timeout: 1, fall: 1}, addr: a.addr}
	checks := func() uint64 {
		st, ok := hc().checker.Status(key1)
		if !ok {
			t.Fatal("no check of a")
		}
		return st.Checks
	}
	session := br.Session(client.Id)
	link := func() {
		target, err := session.OpenLink(conn.NewLink("tcp", a.addr, false, false, "127.0.0.1:9", false), 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = target.Close()
	}
	link()
	time.Sleep(300 * time.Millisecond) // let the link reach npc
	before := checks()
	for range 8 {
		link()
		time.Sleep(250 * time.Millisecond)
	}
	if got := checks(); got != before {
		t.Fatalf("%d probes during traffic", got-before)
	}
	waitFor(t, 5*time.Second, "probe when idle", func() bool { return checks() > before })

	// A new host with the same check shares the running checks.
	host := &model.Host{Id: 7, Host: "x.test", Client: client, Target: &model.Target{TargetStr: list(a, b)}, Flow: &model.Flow{}}
	host.SetHealthCheck(healthCheck())
	if err := db.NewHost(host); err != nil {
		t.Fatal(err)
	}
	wantKeys("group of the host", "h7", sorted(a.addr, b.addr))
	if got := hc().checker.Len(); got != 2 {
		t.Fatalf("%d checks for two groups of equal targets, want 2", got)
	}

	// An edit sends its group again: t1 moves from b to c and b stays for h7.
	edited := tunnel.Clone()
	edited.Target = &model.Target{TargetStr: list(a, c)}
	if err := db.UpdateTask(edited); err != nil {
		t.Fatal(err)
	}
	wantKeys("edited group", "t1", sorted(a.addr, c.addr))
	waitFor(t, 10*time.Second, "three checks", func() bool { return hc().checker.Len() == 3 })

	// A verdict reaches a group that joined a running check, and the new Target.
	c.down()
	waitFor(t, 10*time.Second, "c out of rotation", func() bool { return slices.Equal(rotation(edited.Target), []string{a.addr}) })
	c.up()
	waitFor(t, 10*time.Second, "c back", func() bool { return slices.Equal(rotation(edited.Target), sorted(a.addr, c.addr)) })

	// Removal releases: a host's group goes, the check only it held stops.
	if err := db.DelHost(host.Id); err != nil {
		t.Fatal(err)
	}
	wantKeys("host group gone", "h7", nil)
	waitFor(t, 10*time.Second, "b released", func() bool { return hc().checker.Len() == 2 })

	// A dropped verdict returns: disabling while down puts the target back.
	c.down()
	waitFor(t, 10*time.Second, "c out again", func() bool { return slices.Equal(rotation(edited.Target), []string{a.addr}) })
	off := model.HealthCheck{Type: "tcp"}
	edited.SetHealthCheck(off)
	if err := db.UpdateTask(edited); err != nil {
		t.Fatal(err)
	}
	wantKeys("group disabled", "t1", nil)
	waitFor(t, 10*time.Second, "all released", func() bool { return hc().checker.Len() == 0 })
	waitFor(t, 10*time.Second, "c restored", func() bool { return slices.Equal(rotation(edited.Target), sorted(a.addr, c.addr)) })
	hc().mu.RLock()
	left := len(hc().byAddr)
	hc().mu.RUnlock()
	if left != 0 {
		t.Fatalf("%d addresses left in the index", left)
	}

	// A reconnect gets everything again; npc starts empty.
	c.up()
	edited.SetHealthCheck(healthCheck())
	if err := db.UpdateTask(edited); err != nil {
		t.Fatal(err)
	}
	wantKeys("group enabled", "t1", sorted(a.addr, c.addr))
	stop()
	stopped = true
	rp, stop = start()
	stopped = false
	wantKeys("group after reconnect", "t1", sorted(a.addr, c.addr))
}
