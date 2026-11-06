//go:build !sdk

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
)

func goodProfiles(t *testing.T, name string) []launch.Profile {
	t.Helper()
	p, err := launch.Resolve(context.Background(), `{"name":"`+name+`","server":"a:1","vkey":"k"}`)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fastLaunchDelays(t *testing.T) {
	t.Helper()
	r, p, w, a := launchSourceRetry, launchSourcePause, launchRelaunchWait, client.AutoReconnect
	launchSourceRetry, launchSourcePause, launchRelaunchWait, client.AutoReconnect = time.Millisecond, time.Millisecond, time.Millisecond, true
	t.Cleanup(func() {
		launchSourceRetry, launchSourcePause, launchRelaunchWait, client.AutoReconnect = r, p, w, a
	})
}

// script runs each step of a source's answers, repeating the last one.
func script(steps ...func() ([]launch.Profile, error)) func(context.Context, string) ([]launch.Profile, error) {
	i := 0
	return func(context.Context, string) ([]launch.Profile, error) {
		s := steps[min(i, len(steps)-1)]
		i++
		return s()
	}
}

func TestSuperviseFallsBackToLastGood(t *testing.T) {
	fastLaunchDelays(t)
	launchSourceRetry = time.Hour // the second run must not ask the source again
	good := goodProfiles(t, "good")
	resolves := 0
	resolve := script(
		func() ([]launch.Profile, error) { resolves++; return good, nil },
		func() ([]launch.Profile, error) {
			resolves++
			return nil, &launch.RemoteError{Err: errors.New("down")}
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := 0
	run := func(_ context.Context, p []launch.Profile) error {
		if p[0].Name != "good" {
			t.Errorf("ran %q", p[0].Name)
		}
		if runs++; runs == 3 {
			cancel()
		}
		return nil
	}
	if err := superviseLaunch(ctx, 1, "x", resolve, run); err != nil {
		t.Fatal(err)
	}
	if runs != 3 || resolves != 2 {
		t.Fatalf("runs=%d resolves=%d", runs, resolves)
	}
}

func TestSuperviseRevokedDropsLastGood(t *testing.T) {
	fastLaunchDelays(t)
	good, next := goodProfiles(t, "good"), goodProfiles(t, "next")
	resolve := script(
		func() ([]launch.Profile, error) { return good, nil },
		func() ([]launch.Profile, error) {
			return nil, &launch.RemoteError{Revoked: true, Err: errors.New("gone")}
		},
		func() ([]launch.Profile, error) { return next, nil },
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ran []string
	run := func(_ context.Context, p []launch.Profile) error {
		ran = append(ran, p[0].Name)
		if len(ran) == 2 {
			cancel()
		}
		return nil
	}
	if err := superviseLaunch(ctx, 1, "x", resolve, run); err != nil {
		t.Fatal(err)
	}
	if ran[0] != "good" || ran[1] != "next" {
		t.Fatalf("%v", ran)
	}
}

func TestSuperviseStopsOnInvalidLocalInput(t *testing.T) {
	fastLaunchDelays(t)
	want := errors.New("bad")
	resolve := script(func() ([]launch.Profile, error) { return nil, want })
	err := superviseLaunch(context.Background(), 1, "x", resolve, func(context.Context, []launch.Profile) error { return nil })
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestSuperviseStopsWithoutReconnect(t *testing.T) {
	fastLaunchDelays(t)
	client.AutoReconnect = false
	good := goodProfiles(t, "good")
	resolve := script(func() ([]launch.Profile, error) { return good, nil })
	if err := superviseLaunch(context.Background(), 1, "x", resolve, func(context.Context, []launch.Profile) error { return nil }); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSuperviseCancelDuringWait(t *testing.T) {
	fastLaunchDelays(t)
	launchSourceRetry = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	resolve := script(func() ([]launch.Profile, error) {
		time.AfterFunc(10*time.Millisecond, cancel)
		return nil, &launch.RemoteError{Err: errors.New("down")}
	})
	done := make(chan error, 1)
	go func() {
		done <- superviseLaunch(ctx, 1, "x", resolve, func(context.Context, []launch.Profile) error { return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervise did not stop")
	}
}
