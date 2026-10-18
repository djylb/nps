package service

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/servercfg"
)

func registerTestService(clients ...*file.Client) DefaultClientService {
	cfg := &servercfg.Snapshot{}
	cfg.App.IPRegisterMaxHours = 2
	cfg.Runtime.PublicVKey = "pub-key"
	return DefaultClientService{ConfigProvider: func() *servercfg.Snapshot { return cfg }, Backend: Backend{Repository: stubRepository{
		rangeClients: func(fn func(*file.Client) bool) {
			for _, c := range clients {
				if !fn(c) {
					return
				}
			}
		},
	}}}
}

func TestRegisterVisitorIP(t *testing.T) {
	ipreg.Reset()
	t.Cleanup(ipreg.Reset)
	a := &file.Client{Id: 1, Status: true, VerifyKey: "vk-a", RegisterKey: "reg-a", RegisterMaxHours: 4}
	b := &file.Client{Id: 2, Status: true, VerifyKey: "vk-b"}
	off := &file.Client{Id: 3, Status: false, VerifyKey: "vk-off"}
	s := registerTestService(a, b, off)

	id, until, err := s.RegisterVisitorIP("reg-a", "192.0.2.1", 100)
	if err != nil || id != 1 || time.Until(until) > 4*time.Hour+time.Minute || time.Until(until) < 3*time.Hour {
		t.Fatalf("registration key: id %d, until %v, err %v; want client 1 for its 4 hour limit", id, until, err)
	}
	if !ipreg.Registered(1, netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("address not registered")
	}
	id, until, err = s.RegisterVisitorIP("vk-b", "2001:db8::1", 0)
	if err != nil || id != 2 || time.Until(until) > 2*time.Hour+time.Minute {
		t.Fatalf("verify key: id %d, until %v, err %v; want client 2 for the default 2 hours", id, until, err)
	}
	id, until, err = s.RegisterVisitorIP("pub-key", "192.0.2.5", 1)
	if err != nil || id != ipreg.AnyClient || time.Until(until) > time.Hour+time.Minute || !ipreg.Registered(42, netip.MustParseAddr("192.0.2.5")) {
		t.Fatalf("public key: id %d, until %v, err %v; want every client for 1 hour", id, until, err)
	}
	if _, until, _ = s.RegisterVisitorIP("pub-key", "192.0.2.6", 720); time.Until(until) > 2*time.Hour+time.Minute {
		t.Fatalf("public key registration not cut to the server limit: %v", until)
	}
	for _, key := range []string{"", "wrong", "vk-off", " "} {
		if _, _, err := s.RegisterVisitorIP(key, "192.0.2.9", 1); !errors.Is(err, ErrRegisterKeyInvalid) {
			t.Errorf("key %q: err = %v, want ErrRegisterKeyInvalid", key, err)
		}
	}
	if _, _, err := s.RegisterVisitorIP("reg-a", "not an ip", 1); !errors.Is(err, ErrRegisterKeyInvalid) {
		t.Fatalf("bad address: err = %v", err)
	}
}

func TestCheckRegisterKey(t *testing.T) {
	a := &file.Client{Id: 1, VerifyKey: "vk-a", RegisterKey: "reg-a"}
	s := registerTestService(a)
	for key, dup := range map[string]bool{"": false, "fresh": false, "reg-a": true, "vk-a": true, "vk-self": true} {
		err := s.checkRegisterKey(2, key, "vk-self")
		if got := errors.Is(err, ErrRegisterKeyDuplicate); got != dup {
			t.Errorf("key %q: duplicate = %v, want %v", key, got, dup)
		}
	}
	if err := s.checkRegisterKey(1, "reg-a", "vk-a"); err != nil {
		t.Fatalf("a client's own key reported as duplicate: %v", err)
	}
}
