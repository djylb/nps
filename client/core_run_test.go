package client

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/config"
)

// fakeOuter records how the client uses an outer session.
type fakeOuter struct {
	keep    bool
	hosting func() bool
	closed  atomic.Int32
	dials   atomic.Int32
	visitor atomic.Int32
}

func (o *fakeOuter) Dial(context.Context) (coreapi.ClientSession, error) {
	o.dials.Add(1)
	return nil, errors.New("no server")
}
func (o *fakeOuter) Server() string { return "fake" }
func (o *fakeOuter) Hold(keep bool, hosting func() bool) {
	o.keep, o.hosting = keep, hosting
}
func (o *fakeOuter) Close() { o.closed.Add(1) }
func (o *fakeOuter) Visitor(access []byte, mode string, idle time.Duration) coreapi.Visitor {
	o.visitor.Add(1)
	return &fakeVisitor{access: string(access)}
}

type fakeVisitor struct{ access string }

func (*fakeVisitor) OpenLink(context.Context, coreapi.LinkRequest) (net.Conn, error) {
	return nil, errors.New("fake")
}
func (*fakeVisitor) Close() error { return nil }

// useFakeOuters installs a CoreOuter that records the profile it was asked
// for and hands out a fakeOuter for each.
func useFakeOuters(t *testing.T) (made *[]*config.CommonConfig, outers *[]*fakeOuter) {
	t.Helper()
	old := CoreOuter
	t.Cleanup(func() { CoreOuter = old })
	made, outers = new([]*config.CommonConfig), new([]*fakeOuter)
	CoreOuter = func(cfg *config.CommonConfig, server string) (coreapi.Outer, error) {
		c := *cfg
		c.Server = server
		*made = append(*made, &c)
		o := &fakeOuter{}
		*outers = append(*outers, o)
		return o, nil
	}
	return made, outers
}

func coreTestConfig(t *testing.T, text string) *config.Config {
	t.Helper()
	c, err := config.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every local section of a config shares one outer session with the host of
// the same profile; the session is given up with its last user.
func TestCoreSessionShared(t *testing.T) {
	made, outers := useFakeOuters(t)
	text := "[common]\nserver_addr=127.0.0.1:1;127.0.0.1:2\nvkey=k\nrelay_idle=5\nsession_max_lanes=3\npipes=multi\nscheduler_up=spread_eta\n" +
		"[secret_a]\nlocal_type=tcp\nconn_mode=relay\nlocal_port=1\npassword=a\n" +
		"[secret_b]\nlocal_type=udp\nconn_mode=auto\nlocal_port=2\npassword=b\n"
	c := coreTestConfig(t, text)
	if len(c.LocalServer) != 2 {
		t.Fatalf("%d sections", len(c.LocalServer))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := NewSecretManager(ctx, cancel, c.CommonConfig)
	if err := mgr.UseCore(); err != nil {
		t.Fatal(err)
	}
	va, vb := mgr.visitor(c.LocalServer[0], "relay"), mgr.visitor(c.LocalServer[1], "auto")
	if va == vb || mgr.visitor(c.LocalServer[0], "relay") != va {
		t.Fatal("one visitor for each password")
	}
	if len(*made) != 1 || len(*outers) != 1 || (*outers)[0].visitor.Load() != 2 {
		t.Fatalf("%d outers, %d visitors", len(*outers), (*outers)[0].visitor.Load())
	}
	// the profile's settings are what the factory gets; the first of a list
	// of profiles is the visitors'
	if m := (*made)[0]; m.Server != "127.0.0.1:1" || m.VKey != "k" || m.MaxLanes != 3 || m.Pipes != "multi" || m.SchedulerUp != "spread_eta" || m.RelayIdle != 5 {
		t.Fatalf("%+v", m)
	}
	outer := (*outers)[0]

	// The host of the same profile joins the entry and keeps the session.
	host := *c.CommonConfig
	host.Server = "127.0.0.1:1"
	hctx, hcancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- RunCoreProfile(hctx, &host, true) }()
	waitCore(t, "host running", func() bool { return outer.hosting != nil && outer.hosting() })
	if len(*outers) != 1 {
		t.Fatal("the host made a session of its own")
	}
	hcancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if outer.hosting() || outer.closed.Load() != 0 {
		t.Fatalf("hosting %v, closed %d after the host ended", outer.hosting(), outer.closed.Load())
	}
	mgr.Close()
	if outer.closed.Load() != 1 {
		t.Fatalf("closed %d times", outer.closed.Load())
	}
	coreOuters.mu.Lock()
	n := len(coreOuters.m)
	coreOuters.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d entries left", n)
	}
}

// Profiles that dial differently do not share a session.
func TestCoreSessionsDiffer(t *testing.T) {
	_, outers := useFakeOuters(t)
	base := &config.CommonConfig{Server: "127.0.0.1:1", VKey: "k", Tp: "tcp"}
	a, err := acquireCore(base, base.Server)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := acquireCore(base, base.Server)
	other := *base
	other.MaxLanes = 2
	b, _ := acquireCore(&other, other.Server)
	if a != same || a == b || len(*outers) != 2 {
		t.Fatalf("%v %v %v %d", a, same, b, len(*outers))
	}
	a.release()
	same.release()
	b.release()
	if (*outers)[0].closed.Load() != 1 || (*outers)[1].closed.Load() != 1 {
		t.Fatal("sessions not closed with their last user")
	}
}

func TestCoreKeepServerSession(t *testing.T) {
	_, outers := useFakeOuters(t)
	c := coreTestConfig(t, "[common]\nserver_addr=127.0.0.1:1\nvkey=k\np2p_visitor_keep_server=true\n")
	e, err := acquireCore(c.CommonConfig, c.CommonConfig.Server)
	if err != nil {
		t.Fatal(err)
	}
	o := (*outers)[0]
	waitCore(t, "keeper dialing", func() bool { return o.dials.Load() > 0 })
	if !o.keep {
		t.Fatal("keep not set")
	}
	e.release() // stops the keeper
	if o.closed.Load() != 1 {
		t.Fatal("session not closed")
	}
}

func TestCoreNotAvailable(t *testing.T) {
	old := CoreOuter
	CoreOuter = nil
	t.Cleanup(func() { CoreOuter = old })
	c := &config.CommonConfig{Server: "127.0.0.1:1", VKey: "k", Tp: "tcp"}
	if err := RunCoreProfile(context.Background(), c, false); err == nil {
		t.Fatal("ran without the protocol")
	}
}

func waitCore(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timeout: " + what)
}
