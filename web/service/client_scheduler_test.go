package service

import (
	"errors"
	"testing"
)

func TestClientSchedulerUp(t *testing.T) {
	db := useTempDb(t)
	clients, _ := seedConfig(t, db)
	id, err := clients.Add(AddClientInput{VKey: "vkey-s", WebUserName: "sched", SchedulerUp: " Spread_ETA ", Compress: true, CompressAlg: "zstd"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := clients.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cnf.SchedulerUp != "spread_eta" || c.Cnf.CompressAlg != "zstd" {
		t.Fatalf("stored %+v", c.Cnf)
	}
	edit := func(up string) error {
		return clients.Edit(EditClientInput{ID: id, IsAdmin: true, VKey: "vkey-s", WebUserName: "sched", SchedulerUp: up})
	}
	if err := edit("bogus"); !errors.Is(err, ErrInvalidScheduler) {
		t.Fatalf("invalid value: %v", err)
	}
	if c.Cnf.SchedulerUp != "spread_eta" {
		t.Fatal("a rejected edit changed the client")
	}
	if err := edit("affinity"); err != nil || c.Cnf.SchedulerUp != "affinity" {
		t.Fatalf("affinity: %v %q", err, c.Cnf.SchedulerUp)
	}
	if err := edit(""); err != nil || c.Cnf.SchedulerUp != "" {
		t.Fatalf("unset: %v %q", err, c.Cnf.SchedulerUp)
	}
	if _, err := clients.Add(AddClientInput{VKey: "vkey-t", WebUserName: "sched2", SchedulerUp: "bogus"}); !errors.Is(err, ErrInvalidScheduler) {
		t.Fatalf("add invalid: %v", err)
	}
}

// An edit that carries no crypt setting keeps the stored one.
func TestClientEditKeepsCrypt(t *testing.T) {
	db := useTempDb(t)
	clients, _ := seedConfig(t, db)
	id, err := clients.Add(AddClientInput{VKey: "vkey-c", WebUserName: "crypt", Crypt: true})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := clients.Get(id)
	edit := func(crypt *bool) {
		if err := clients.Edit(EditClientInput{ID: id, IsAdmin: true, VKey: "vkey-c", WebUserName: "crypt", Crypt: crypt}); err != nil {
			t.Fatal(err)
		}
	}
	off, on := false, true
	edit(nil)
	if !c.Cnf.Crypt {
		t.Fatal("an edit without crypt switched it off")
	}
	edit(&off)
	if c.Cnf.Crypt {
		t.Fatal("crypt=0 not applied")
	}
	edit(nil)
	edit(&on)
	if !c.Cnf.Crypt {
		t.Fatal("crypt=1 not applied")
	}
}
