package session

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/connprofile"
)

func parseAuto(t *testing.T, s string) []connprofile.Candidate {
	t.Helper()
	c, err := connprofile.Parse(s, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newAutoDialer(t *testing.T, s string) *Dialer {
	return NewDialer(DialConfig{Candidates: parseAuto(t, s), VKey: "k", Timeout: 2 * time.Second})
}

func TestAutoExpansion(t *testing.T) {
	carriers := func(s string) (out []string) {
		for _, c := range parseAuto(t, s) {
			out = append(out, c.Carrier)
		}
		return out
	}
	if got := carriers("auto://h:1/p"); !slices.Equal(got, []string{"tls", "quic", "wss", "https"}) {
		t.Fatal(got)
	}
	if got := carriers("auto://h:1"); !slices.Equal(got, []string{"tls", "quic"}) {
		t.Fatal(got)
	}
	if _, err := connprofile.Parse("auto://h/p", ""); err == nil {
		t.Fatal("auto without a port")
	}
}

// succeed is what the first attempt to get the gate does.
func succeed(ctx context.Context) (*clientSession, error) {
	if err := raceGateOf(ctx).acquire(ctx); err != nil {
		return nil, err
	}
	return &clientSession{}, nil
}

func TestRaceBlackholedFirstThenSecondAfterStagger(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1")
	start := time.Now()
	var second time.Duration
	d.attempt = func(ctx context.Context, c connprofile.Candidate) (*clientSession, error) {
		if c.Carrier == "tls" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		second = time.Since(start)
		return succeed(ctx)
	}
	s, c, err := d.raceSet(context.Background(), splitGroups(d))
	if err != nil || s == nil || c.Carrier != "quic" {
		t.Fatal(c.Carrier, err)
	}
	if second < raceStagger-10*time.Millisecond {
		t.Fatalf("second started after %v", second)
	}
}

func splitGroups(d *Dialer) [][]connprofile.Candidate {
	first, _ := d.raceGroups(d.attempts(d.cfg.Candidates), "n")
	return first
}

func TestRaceOneWinnerWhenSimultaneous(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1/p?http=http/1.1&kinds=streaming")
	var commits atomic.Int32
	release := make(chan struct{})
	d.attempt = func(ctx context.Context, _ connprofile.Candidate) (*clientSession, error) {
		time.Sleep(time.Millisecond)
		<-release
		s, err := succeed(ctx)
		if err == nil {
			commits.Add(1)
		}
		return s, err
	}
	go func() { time.Sleep(3 * raceStagger); close(release) }()
	if _, _, err := d.raceSet(context.Background(), splitGroups(d)); err != nil {
		t.Fatal(err)
	}
	if n := commits.Load(); n != 1 {
		t.Fatalf("%d committed", n)
	}
}

func TestRaceHolderFailureLetsTheNextGo(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1")
	holder := make(chan struct{})
	d.attempt = func(ctx context.Context, c connprofile.Candidate) (*clientSession, error) {
		g := raceGateOf(ctx)
		if c.Carrier == "tls" {
			if err := g.acquire(ctx); err != nil {
				return nil, err
			}
			defer g.release()
			<-holder
			return nil, errors.New("rejected")
		}
		return succeed(ctx)
	}
	go func() { time.Sleep(2 * raceStagger); close(holder) }()
	_, c, err := d.raceSet(context.Background(), splitGroups(d))
	if err != nil || c.Carrier != "quic" {
		t.Fatal(c.Carrier, err)
	}
}

func TestAutoCacheAndStandbyOrder(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1/p?http=http/1.1&kinds=streaming")
	cands := d.attempts(d.cfg.Candidates)
	raw := func(g [][]connprofile.Candidate) (out []string) {
		for _, x := range g {
			out = append(out, x[0].Carrier)
		}
		return out
	}
	g, _ := d.raceGroups(cands, "n")
	if !slices.Equal(raw(g), []string{"tls", "quic", "wss", "https"}) {
		t.Fatal(raw(g))
	}
	wss := g[2][0].Raw
	d.autoOK[autoKey(wss, "n")] = true
	if g, _ = d.raceGroups(cands, "n"); raw(g)[0] != "wss" {
		t.Fatal(raw(g))
	}
	// Another network has no memory.
	if g, _ = d.raceGroups(cands, "m"); raw(g)[0] != "tls" {
		t.Fatal(raw(g))
	}
	withStandby := parseAuto(t, "auto://h:1?standby=true|tls://b:2")
	first, standby := (&Dialer{autoOK: map[string]bool{}, autoDropped: map[string]bool{}}).raceGroups(connprofile.Attempts(withStandby), "n")
	if len(first) != 1 || first[0][0].Addr != "b:2" || len(standby) != 2 {
		t.Fatal(raw(first), raw(standby))
	}
}

// A session that broke right after connecting moves its entry to the back.
func TestAutoQuickDropIsNotPreferred(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1")
	cands := d.attempts(d.cfg.Candidates)
	key := autoKey(cands[1].Raw, "n") // quic
	d.autoOK[key] = true
	start := d.now()
	d.autoEnded(&clientSession{autoEntry: key, autoStart: start})
	if d.autoOK[key] || !d.autoDropped[key] {
		t.Fatal("a quick drop stayed good")
	}
	g, _ := d.raceGroups(cands, "n")
	if g[len(g)-1][0].Raw != cands[1].Raw {
		t.Fatal("dropped entry not last")
	}
	// A session past the window clears it.
	d.autoEnded(&clientSession{autoEntry: key, autoStart: start.Add(-2 * autoDropWindow)})
	if d.autoDropped[key] {
		t.Fatal("still dropped")
	}
}

func TestRaceAllFailIsOneError(t *testing.T) {
	d := newAutoDialer(t, "auto://h:1")
	d.attempt = func(context.Context, connprofile.Candidate) (*clientSession, error) {
		return nil, errors.New("no")
	}
	if _, err := d.dialRace(context.Background(), d.attempts(d.cfg.Candidates)); err == nil {
		t.Fatal("no error")
	}
}
