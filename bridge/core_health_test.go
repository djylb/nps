package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/model"
)

// The server hands npc the checks of an entry with CONFIG, and a dead target
// reported on the HEALTH stream leaves the rotation.
func TestHealthOnCoreSession(t *testing.T) {
	f := newCoreFixture(t, 0, model.Config{})
	live, dead := echoListener(t), "127.0.0.1:1"
	tn := &model.Tunnel{Id: f.db.NextTunnelID(), Mode: "tcp", Status: true, Client: f.client, Flow: new(model.Flow), Port: freeLocalPort(t),
		Target: &model.Target{TargetStr: live + "\n" + dead}}
	tn.SetHealthCheck(model.HealthCheck{Enabled: true, Interval: 1, Timeout: 1, MaxFail: 1})
	if err := f.db.NewTask(tn); err != nil {
		t.Fatal(err)
	}
	cands, err := connprofile.Parse(f.addr, "tcp")
	if err != nil {
		t.Fatal(err)
	}
	s, err := session.NewDialer(session.DialConfig{Candidates: cands, VKey: f.client.VerifyKey, Agent: "npc/9.8.7 test/x",
		Exit: client.NewCoreExit("", 0), Health: client.NewCoreHealth, Timeout: 4 * time.Second}).Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(); <-s.Done() }()

	group := tunnelHealthGroup(tn.Id)
	downNow := func() (d bool) {
		f.b.corePushes.Range(func(_, v any) bool {
			p := v.(*healthPush)
			p.mu.Lock()
			d = p.down[group][dead]
			p.mu.Unlock()
			return false
		})
		return
	}
	for end := time.Now().Add(10 * time.Second); !downNow(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("dead target was not reported down")
		}
	}
}
