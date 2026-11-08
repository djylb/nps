package client

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/acl"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
)

// spyExit records what it was asked to dial or relay.
type spyExit struct {
	dialed []string
	udp    int
	policy conn.UDP5Policy
}

func (s *spyExit) Dial(_ context.Context, _, target string) (net.Conn, error) {
	s.dialed = append(s.dialed, target)
	a, _ := net.Pipe()
	return a, nil
}
func (s *spyExit) ServeUDP5(context.Context, net.Conn) { s.udp++ }
func (s *spyExit) ServeUDP5Policy(_ context.Context, _ net.Conn, p conn.UDP5Policy) {
	s.udp++
	s.policy = p
}

func peerExitOf(t *testing.T, base coreapi.Exit, lp coreapi.LinkPolicy, names map[string][]string) *peerExit {
	t.Helper()
	x, err := NewCorePeerExit(base)(lp)
	if err != nil {
		t.Fatal(err)
	}
	e := x.(*peerExit)
	e.resolve = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range names[host] {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
	return e
}

// Mixed rule text: a deny line is a deny, not an allow (§9.5).
func TestPeerScopeMixedDenyIsNotAllow(t *testing.T) {
	spy := &spyExit{}
	e := peerExitOf(t, spy, coreapi.LinkPolicy{NameDest: true, Scope: "deny bad.example\nallow 10.0.0.0/8"},
		map[string][]string{"bad.example": {"10.0.0.1"}, "ok.example": {"10.0.0.2"}})
	if _, err := e.Dial(context.Background(), "tcp", "bad.example:80"); !errors.Is(err, acl.ErrScopeDenied) {
		t.Fatalf("denied name: %v", err)
	}
	if _, err := e.Dial(context.Background(), "tcp", "ok.example:80"); err != nil {
		t.Fatal(err)
	}
	if len(spy.dialed) != 1 {
		t.Fatalf("dialed %v", spy.dialed)
	}
}

func TestPeerScopeOnlyComments(t *testing.T) {
	if _, err := NewCorePeerExit(&spyExit{})(coreapi.LinkPolicy{NameDest: true, Scope: "# nothing\n"}); !errors.Is(err, acl.ErrMalformedScope) {
		t.Fatalf("err = %v", err)
	}
}

// A domain is judged by what it resolves to and dialed at that address, so
// that a later DNS answer cannot move it.
func TestPeerScopeResolvesAndBinds(t *testing.T) {
	spy := &spyExit{}
	e := peerExitOf(t, spy, coreapi.LinkPolicy{NameDest: true, Scope: "deny 0.0.0.0/0\ndeny ::/0"},
		map[string][]string{"a.example": {"1.1.1.1"}})
	if _, err := e.Dial(context.Background(), "tcp", "a.example:80"); !errors.Is(err, acl.ErrScopeDenied) || len(spy.dialed) != 0 {
		t.Fatalf("deny of addresses: %v, dialed %v", err, spy.dialed)
	}
	spy = &spyExit{}
	e = peerExitOf(t, spy, coreapi.LinkPolicy{NameDest: true, Scope: "allow 10.0.0.0/8"},
		map[string][]string{"in.example": {"10.0.0.5"}, "mixed.example": {"10.0.0.5", "8.8.8.8"}})
	if _, err := e.Dial(context.Background(), "tcp", "mixed.example:80"); !errors.Is(err, acl.ErrScopeDenied) {
		t.Fatalf("one address outside: %v", err)
	}
	if _, err := e.Dial(context.Background(), "tcp", "in.example:80"); err != nil {
		t.Fatal(err)
	}
	if len(spy.dialed) != 1 || spy.dialed[0] != "10.0.0.5:80" {
		t.Fatalf("dialed %v", spy.dialed)
	}
	calls := 0
	e = peerExitOf(t, &spyExit{}, coreapi.LinkPolicy{NameDest: true, Scope: "deny *"}, nil)
	e.resolve = func(context.Context, string, string) ([]netip.Addr, error) { calls++; return nil, nil }
	if _, err := e.Dial(context.Background(), "tcp", "a.example:80"); !errors.Is(err, acl.ErrScopeDenied) || calls != 0 {
		t.Fatalf("deny *: %v, lookups %d", err, calls)
	}
}

// §9.3: a named target must also match the scope without name_dest.
func TestPeerScopeAppliesToTargets(t *testing.T) {
	spy := &spyExit{}
	e := peerExitOf(t, spy, coreapi.LinkPolicy{Targets: []string{"10.0.0.1:80"}, Scope: "deny 10.0.0.0/8"}, nil)
	if _, err := e.Dial(context.Background(), "tcp", "10.0.0.1:80"); !errors.Is(err, acl.ErrScopeDenied) || len(spy.dialed) != 0 {
		t.Fatalf("%v %v", err, spy.dialed)
	}
}

func TestPeerUDP5Policy(t *testing.T) {
	spy := &spyExit{}
	e := peerExitOf(t, spy, coreapi.LinkPolicy{Targets: []string{"1.1.1.1:53"}}, nil)
	if err := e.AdmitUDP5(); err == nil {
		t.Fatal("addressed UDP admitted without name_dest")
	}
	e = peerExitOf(t, spy, coreapi.LinkPolicy{NameDest: true, TargetType: coreapi.NetworkTCP}, nil)
	if err := e.AdmitUDP5(); err == nil {
		t.Fatal("addressed UDP admitted on a tcp task")
	}
	e = peerExitOf(t, spy, coreapi.LinkPolicy{NameDest: true, Scope: "deny 10.0.0.0/8\ndeny bad.example"}, nil)
	if err := e.AdmitUDP5(); err != nil {
		t.Fatal(err)
	}
	e.ServeUDP5(context.Background(), nil)
	if spy.policy == nil {
		t.Fatal("no per-datagram policy")
	}
	ten := []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	if spy.policy("1.2.3.4:53", []netip.Addr{netip.MustParseAddr("1.2.3.4")}) != true ||
		spy.policy("10.0.0.1:53", ten) != false ||
		spy.policy("bad.example:53", nil) != false ||
		spy.policy("a.example:53", nil) != true ||
		spy.policy("a.example:53", ten) != false {
		t.Fatal("per-datagram policy wrong")
	}
}

// fakeSession is a session that records Close and publishes.
type fakeSession struct {
	coreapi.ClientSession
	done      chan struct{}
	closed    int
	published [][]coreapi.LocalService
}

func newFakeSession() *fakeSession           { return &fakeSession{done: make(chan struct{})} }
func (s *fakeSession) Done() <-chan struct{} { return s.done }
func (s *fakeSession) Err() error            { return nil }
func (s *fakeSession) Carrier() string       { return "fake" }
func (s *fakeSession) Close() error {
	if s.closed++; s.closed == 1 {
		close(s.done)
	}
	return nil
}
func (s *fakeSession) PublishServices(_ context.Context, l []coreapi.LocalService) error {
	s.published = append(s.published, l)
	return nil
}

type sessionOuter struct {
	fakeOuter
	s *fakeSession
}

func (o *sessionOuter) Dial(context.Context) (coreapi.ClientSession, error) { return o.s, nil }

func useSessionOuter(t *testing.T, s *fakeSession) {
	t.Helper()
	old := CoreOuter
	t.Cleanup(func() { CoreOuter = old })
	CoreOuter = func(*config.CommonConfig, string) (coreapi.Outer, error) { return &sessionOuter{s: s}, nil }
}

// A host stopping or failing leaves a session the visitors still use.
func TestSharedSessionOutlivesHost(t *testing.T) {
	s := newFakeSession()
	useSessionOuter(t, s)
	cfg := &config.CommonConfig{Server: "x", VKey: "k"}
	e, err := acquireCore(cfg, "x") // a visitor
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runCoreProfile(ctx, cfg, false, nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err = runCoreProfile(context.Background(), cfg, false, func(context.Context, coreapi.ClientSession, *coreHost) error { return boom })
	if !errors.Is(err, boom) || s.closed != 0 {
		t.Fatalf("err %v, closed %d", err, s.closed)
	}
	e.release()
}

func TestSharedSessionPublishesAllHosts(t *testing.T) {
	s := newFakeSession()
	e := &coreEntry{users: 2}
	h1, h2 := &coreHost{e: e}, &coreHost{e: e}
	a, b := coreapi.LocalService{Kind: "k", Name: "a"}, coreapi.LocalService{Kind: "k", Name: "b"}
	if err := h1.publish(context.Background(), s, []coreapi.LocalService{a}); err != nil {
		t.Fatal(err)
	}
	if err := h2.publish(context.Background(), s, []coreapi.LocalService{b}); err != nil {
		t.Fatal(err)
	}
	if got := s.published[len(s.published)-1]; len(got) != 2 {
		t.Fatalf("published %v", got)
	}
	h2.withdraw()
	if got := s.published[len(s.published)-1]; len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("after withdraw %v", got)
	}
}

func TestIssuedKeyUsedOnLaterRuns(t *testing.T) {
	made, _ := useFakeOuters(t)
	cnf := &config.Config{CommonConfig: config.NewCommonConfig(), Tasks: nil}
	cnf.CommonConfig.Server, cnf.CommonConfig.VKey = "x", "public"
	cnf.Hosts = append(cnf.Hosts, nil)
	cnf.Hosts = cnf.Hosts[:1]
	issued, err := runCoreConfig(context.Background(), cnf, "issued-key")
	if err != nil || issued != "issued-key" {
		t.Fatalf("%q %v", issued, err)
	}
	if len(*made) != 1 || (*made)[0].VKey != "issued-key" {
		t.Fatalf("dialed with %+v", (*made)[0].VKey)
	}
}

func TestSectionsDifferByConnMode(t *testing.T) {
	var n int
	mgr := &SecretManager{coreVisitor: func(string, string) coreapi.Visitor { n++; return &fakeVisitor{} }}
	l := &config.LocalServer{Password: "p"}
	if mgr.visitor(l, "relay") == mgr.visitor(l, "direct") || n != 2 {
		t.Fatalf("modes share a visitor, made %d", n)
	}
	if mgr.visitor(l, "relay"); n != 2 {
		t.Fatal("same mode not shared")
	}
}
