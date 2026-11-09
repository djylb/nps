//go:build !sdk

package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	flag "github.com/spf13/pflag"
)

func goodProfiles(t *testing.T, links ...string) []launch.Profile {
	t.Helper()
	var out []launch.Profile
	for _, l := range links {
		p, err := launch.Resolve(context.Background(), l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p...)
	}
	return out
}

func fastLaunchDelays(t *testing.T) {
	t.Helper()
	w, a := launch.RelaunchWait, client.AutoReconnect
	launch.RelaunchWait, client.AutoReconnect = time.Millisecond, true
	t.Cleanup(func() { launch.RelaunchWait, client.AutoReconnect = w, a })
}

func TestSuperviseRestartsOnlyTheConnectionThatStopped(t *testing.T) {
	fastLaunchDelays(t)
	profiles := goodProfiles(t, "npc://k@a:1#stay", "npc://k@b:1#flap")
	load := func(context.Context) (launch.Plan, error) { return launch.Plan{Profiles: profiles}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	runs := map[string]int{}
	run := func(ctx context.Context, p launch.Profile) error {
		mu.Lock()
		runs[p.Name]++
		n := runs[p.Name]
		mu.Unlock()
		if p.Name == "flap" && n < 3 {
			return errors.New("lost")
		}
		if p.Name == "flap" {
			cancel()
		}
		<-ctx.Done()
		return nil
	}
	if err := launch.Supervise(ctx, load, run); err != nil {
		t.Fatal(err)
	}
	if runs["stay"] != 1 || runs["flap"] != 3 {
		t.Fatalf("%v", runs)
	}
}

func TestSuperviseFirstLoadInvalid(t *testing.T) {
	want := errors.New("bad")
	err := launch.Supervise(context.Background(), func(context.Context) (launch.Plan, error) { return launch.Plan{}, want },
		func(context.Context, launch.Profile) error { return nil })
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestSuperviseLaterInvalidLoadKeepsGoing(t *testing.T) {
	fastLaunchDelays(t)
	r := launch.SourceRetry
	launch.SourceRetry = time.Millisecond
	t.Cleanup(func() { launch.SourceRetry = r })
	profiles := goodProfiles(t, "npc://k@a:1")
	var loads atomic.Int32
	load := func(context.Context) (launch.Plan, error) {
		if loads.Add(1) == 2 {
			return launch.Plan{}, errors.New("bad now")
		}
		return launch.Plan{Profiles: profiles}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	run := func(ctx context.Context, _ launch.Profile) error {
		if runs.Add(1) == 2 {
			cancel()
			<-ctx.Done()
		}
		return nil
	}
	if err := launch.Supervise(ctx, load, run); err != nil {
		t.Fatal(err)
	}
	if loads.Load() < 3 {
		t.Fatal(loads.Load())
	}
}

func TestSuperviseStopsWithoutReconnect(t *testing.T) {
	fastLaunchDelays(t)
	client.AutoReconnect = false
	profiles := goodProfiles(t, "npc://k@a:1")
	err := launch.Supervise(context.Background(), func(context.Context) (launch.Plan, error) { return launch.Plan{Profiles: profiles}, nil },
		func(context.Context, launch.Profile) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestSupervisePendingRetries(t *testing.T) {
	fastLaunchDelays(t)
	profiles := goodProfiles(t, "npc://k@a:1")
	var loads atomic.Int32
	load := func(context.Context) (launch.Plan, error) {
		if loads.Add(1) == 1 {
			return launch.Plan{Retry: time.Millisecond}, nil
		}
		return launch.Plan{Profiles: profiles}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- launch.Supervise(ctx, load, func(ctx context.Context, _ launch.Profile) error { cancel(); <-ctx.Done(); return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervise did not retry")
	}
}

func TestCommandConnsFromLaunch(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("server", "", "")
	fs.String("vkey", "", "")
	conns, err := commandConns(fs, []string{
		"npc://k1@203.0.113.10:8024?type=tls#a",
		"npc://k2@203.0.113.11:8024?type=auto#b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 2 || conns[0].name != "a" || conns[1].name != "b" {
		t.Fatalf("connections: %+v", conns)
	}
	if c := conns[0].cfg; c.VKey != "k1" || !strings.HasPrefix(c.Server, "tls://203.0.113.10") {
		t.Fatalf("first connection: %+v", c)
	}
	if conns[1].cfg.VKey != "k2" || !strings.HasPrefix(conns[1].cfg.Server, "auto://") {
		t.Fatalf("second connection: %+v", conns[1].cfg)
	}
	if err := fs.Set("vkey", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := commandConns(fs, []string{"npc://k@a:1"}); err == nil {
		t.Fatal("-launch with -vkey must be refused")
	}
}
