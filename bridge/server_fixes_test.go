package bridge

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
)

func TestNewConfigWithoutCnf(t *testing.T) {
	db := useTestDb(t)
	b := NewTunnel(db, &sync.Map{}, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	clientSide, serverSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(conn.NewConn(serverSide), true, nil, legacy.CapsOf(legacy.LatestIndex, ""), "node-1")
	}()
	c := conn.NewConn(clientSide)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	sent := model.NewClient("v", true, true)
	sent.Cnf = nil
	if err := legacy.WriteClient(c, sent); err != nil {
		t.Fatal(err)
	}
	if ok, _ := legacy.ReadStatus(c); !ok {
		t.Fatal("config was not accepted")
	}
	if _, err := io.ReadFull(c, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.Close()
	<-done
	var id int
	b.Client.Range(func(k, _ any) bool { id = k.(int); return false })
	stored, err := db.GetClient(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Cnf == nil {
		t.Fatal("a client from the wire has no Cnf: relays dereference it")
	}
}

// taskStore is a store of one secret task.
type taskStore struct {
	Store
	t *model.Tunnel
}

func (s taskStore) GetTaskByAccessID(id []byte) *model.Tunnel {
	if string(id) == string(coreapi.AccessID([]byte(s.t.Password))) {
		return s.t
	}
	return nil
}

func secretFixture(t *testing.T) (*Bridge, *model.Tunnel) {
	t.Helper()
	task := &model.Tunnel{Id: 3, Mode: "secret", Password: "pw", Status: true,
		Client: &model.Client{Id: 2, Cnf: &model.Config{}, Flow: &model.Flow{}}, Flow: &model.Flow{},
		Target: &model.Target{TargetStr: "127.0.0.1:1"}}
	return NewTunnel(taskStore{Store: useTestDb(t), t: task}, &sync.Map{}, Options{CoreEntry: stubEntry{}, DisconnectTime: 30}), task
}

func accessOf(password string) coreapi.LinkResolve {
	return coreapi.LinkResolve{AccessID: coreapi.AccessID([]byte(password)), Source: netip.MustParseAddrPort("192.0.2.1:9"),
		Verify: func(p []byte) bool { return string(p) == password }}
}

// The provider gets the default target and no limit without a list.
func TestLinkPolicyDefaultOpen(t *testing.T) {
	b, _ := secretFixture(t)
	g, err := b.coreResolveLink(1, accessOf("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Policy.NameDest || g.Policy.Scope != "" || len(g.Policy.Targets) != 1 {
		t.Fatalf("%+v", g.Policy)
	}
}

func TestPipeAdmissionTakesConnectionSlots(t *testing.T) {
	b, task := secretFixture(t)
	task.Client.MaxConn = 1
	g, err := b.coreResolveLink(1, accessOf("pw"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := g.Admit(func() {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Admit(func() {}); err == nil {
		t.Fatal("a second PIPE passed max_conn 1")
	}
	if _, err := b.coreResolveLink(1, accessOf("pw")); err == nil {
		t.Fatal("a LINK passed a full client")
	}
	release()
	r2, err := g.Admit(func() {})
	if err != nil {
		t.Fatalf("slot not given back: %v", err)
	}
	r2()
}

func TestQuotaEndsGrantAndCountsAsItGoes(t *testing.T) {
	b, task := secretFixture(t)
	task.Client.Flow.FlowLimit = 1
	g, err := b.coreResolveLink(1, accessOf("pw"))
	if err != nil {
		t.Fatal(err)
	}
	up, down := g.Limiters()
	up.Charge(100)
	down.Charge(50)
	if v := task.Flow.Values(); v.InletFlow != 100 || v.ExportFlow != 50 {
		t.Fatalf("tunnel flow %+v", v)
	}
	if v := task.Client.Flow.Values(); v.InletFlow != 100 || v.ExportFlow != 50 {
		t.Fatalf("client flow %+v", v)
	}
	if !g.Valid() {
		t.Fatal("under quota and not valid")
	}
	up.Charge(2 << 20)
	if g.Valid() {
		t.Fatal("over quota and still valid")
	}
	if _, err := g.Admit(func() {}); err == nil {
		t.Fatal("a PIPE passed an exhausted quota")
	}
	if _, err := up.Ready(t.Context(), 1, 10); err == nil {
		t.Fatal("a running PIPE goes on over quota")
	}
	if _, err := b.coreResolveLink(1, accessOf("pw")); err == nil {
		t.Fatal("a LINK passed an exhausted quota")
	}
}

func TestExpiryStopsPipe(t *testing.T) {
	b, task := secretFixture(t)
	task.Client.Flow.TimeLimit = time.Now().Add(150 * time.Millisecond)
	g, err := b.coreResolveLink(1, accessOf("pw"))
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	release, err := g.Admit(func() { close(stopped) })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("an expired PIPE runs on")
	}
}

func TestAccessGuessesAreLimitedPerClient(t *testing.T) {
	b, _ := secretFixture(t)
	var checked atomic.Int64
	bad := accessOf("pw")
	bad.Verify = func([]byte) bool { checked.Add(1); return false }
	for range accessGuessBurst * 3 {
		_, _ = b.coreResolveLink(1, bad)
	}
	if n := checked.Load(); n > accessGuessBurst+2 {
		t.Fatalf("%d guesses checked, burst %d", n, accessGuessBurst)
	}
	if _, err := b.coreResolveLink(1, accessOf("pw")); err == nil {
		t.Fatal("a client out of guesses got in")
	}
	if _, err := b.coreResolveLink(9, accessOf("pw")); err != nil {
		t.Fatalf("another client was limited: %v", err)
	}
}

func TestEmptyWhitelistIsDenyAll(t *testing.T) {
	if got := scopeOfACL(model.AclWhitelist, "# only a comment\n"); got != "deny *\n" {
		t.Fatalf("scope %q", got)
	}
}

type deniedStore struct {
	Store
	ip string
}

func (s deniedStore) GlobalDenied(a net.Addr, _ string) bool {
	h, _, _ := net.SplitHostPort(a.String())
	return h == s.ip
}

func TestDeniedHTTPBehindTrustedProxy(t *testing.T) {
	check := func(t *testing.T, header, peer string, set map[string]string, wantDenied bool) {
		t.Helper()
		old := connection.Network()
		connection.Configure(servercfg.NetworkConfig{BridgeTrustedIPs: "127.0.0.1", BridgeRealIPHeader: header})
		t.Cleanup(func() { connection.Configure(old) })
		f := newCoreFixture(t, SelectMode(0), model.Config{Crypt: true})
		f.b.store = deniedStore{Store: f.b.store, ip: "198.51.100.9"}
		h := f.b.deniedHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		for k, v := range set {
			r.Header.Set(k, v)
		}
		denied := false
		func() {
			defer func() {
				if e := recover(); e != nil {
					if e != http.ErrAbortHandler {
						panic(e)
					}
					denied = true
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), r)
		}()
		if denied != wantDenied {
			t.Fatalf("denied %v, want %v", denied, wantDenied)
		}
	}
	xff := map[string]string{"X-Forwarded-For": "198.51.100.9"}
	t.Run("forwarded", func(t *testing.T) { check(t, "", "127.0.0.1:5", xff, true) })
	t.Run("real ip", func(t *testing.T) { check(t, "", "127.0.0.1:5", map[string]string{"X-Real-IP": "198.51.100.9"}, true) })
	t.Run("named header", func(t *testing.T) {
		check(t, "X-Client", "127.0.0.1:5", map[string]string{"X-Client": "198.51.100.9"}, true)
	})
	t.Run("untrusted peer", func(t *testing.T) { check(t, "", "203.0.113.1:5", xff, false) })
}

type fakeCore struct {
	coreapi.Session
	id     string
	client int
	closed bool
}

func (f *fakeCore) Instance() string { return f.id }
func (f *fakeCore) ClientID() int    { return f.client }
func (f *fakeCore) Closed() bool     { return f.closed }
func (f *fakeCore) Suspended() bool  { return false }
func (f *fakeCore) Close() error     { f.closed = true; return nil }
func (f *fakeCore) CanHealth() bool  { return false }

func coreNode(cs *fakeCore) *Node {
	n := NewNode(coreNodeUUID(cs), "1", legacy.LatestIndex)
	n.AddCore(cs)
	return n
}

// Two processes of one vkey report one service: it lives while either does.
func TestServiceOfTwoInstances(t *testing.T) {
	a, b := coreNode(&fakeCore{id: "a", client: 1}), coreNode(&fakeCore{id: "b", client: 1})
	c := NewClient(1, a, Primary)
	c.AddNode(b)
	t.Cleanup(func() { _ = c.Close() })
	for _, n := range []*Node{a, b} {
		if err := c.setNodeServices(n.UUID, []string{"s"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.setNodeServices(b.UUID, nil); err != nil {
		t.Fatal(err)
	}
	if n, ok := c.nodeOfService("s"); !ok || n != a {
		t.Fatalf("the later report took the service: %v %v", n, ok)
	}
	_ = c.setNodeServices(b.UUID, []string{"s"})
	if !c.closeAndRemoveNodeIfCurrent(b.UUID, b, func(*Node) bool { return true }) {
		t.Fatal("not removed")
	}
	if n, ok := c.nodeOfService("s"); !ok || n != a {
		t.Fatalf("removing one node dropped the other's service: %v %v", n, ok)
	}
	if !c.closeAndRemoveNodeIfCurrent(a.UUID, a, func(*Node) bool { return true }) {
		t.Fatal("not removed")
	}
	if _, ok := c.services["s"]; ok {
		t.Fatal("service kept with no node")
	}
}

func coreBridge(t *testing.T, sessions ...*fakeCore) *Bridge {
	t.Helper()
	b, _ := secretFixture(t)
	c := NewClient(1, coreNode(sessions[0]), Primary)
	for _, s := range sessions[1:] {
		c.AddNode(coreNode(s))
	}
	b.Client.Store(1, c)
	t.Cleanup(func() { _ = c.Close() })
	return b
}

func TestNodeServicesLimit(t *testing.T) {
	cs := &fakeCore{id: "a", client: 1}
	b := coreBridge(t, cs)
	b.maxNodeServices = 1
	two := []coreapi.ServiceInfo{{Kind: "file", Name: "a"}, {Kind: "file", Name: "b"}}
	b.coreServices(cs, two)
	if _, ok := b.coreServicesOf.Load(cs); ok {
		t.Fatal("a snapshot over the limit was taken")
	}
	b.maxNodeServices = -1
	b.coreServices(cs, two)
	if _, ok := b.coreServicesOf.Load(cs); !ok {
		t.Fatal("negative is no limit")
	}
}

func TestServicesOfClosedSessionNotKept(t *testing.T) {
	cs := &fakeCore{id: "a", client: 1}
	b := coreBridge(t, cs)
	cs.closed = true
	b.coreServices(cs, []coreapi.ServiceInfo{{Kind: "file", Name: "a"}})
	if _, ok := b.coreServicesOf.Load(cs); ok {
		t.Fatal("the services of an ended session stay listed")
	}
}

func TestDeletedSessionKeepsClientWithAnotherLive(t *testing.T) {
	gone, live := &fakeCore{id: "a", client: 1, closed: true}, &fakeCore{id: "b", client: 1}
	b := coreBridge(t, gone, live)
	b.coreOffline(gone, "deleted")
	if _, ok := b.Client.Load(1); !ok {
		t.Fatal("the client went with a live session left")
	}
	live.closed = true
	b.coreOffline(live, "deleted")
	if _, ok := b.Client.Load(1); ok {
		t.Fatal("the last session left the client")
	}
}
