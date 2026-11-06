package bridge

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestRegistryConnectionHistory(t *testing.T) {
	const id = 987654
	db := file.GetDb()
	c := &file.Client{Id: id, ConnectionHistory: new(file.ConnectionHistory)}
	old, existed := db.JsonDb.Clients.Load(id)
	db.JsonDb.Clients.Store(id, c)
	defer func() {
		if existed {
			db.JsonDb.Clients.Store(id, old)
		} else {
			db.JsonDb.Clients.Delete(id)
		}
	}()
	b := NewTunnel(&sync.Map{}, 30)
	first := NewClient(id, NewNode("one", "", 8))
	b.loadOrStoreClient(id, first)
	b.loadOrStoreClient(id, NewClient(id, NewNode("two", "", 8)))
	b.DelClient(id)
	b.DelClient(id)
	b.loadOrStoreClient(id, NewClient(id, NewNode("three", "", 8)))
	b.DelClient(id)
	raw, err := json.Marshal(c.ConnectionHistory)
	if err != nil {
		t.Fatal(err)
	}
	var events []file.ConnectionEvent
	if err = json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("history = %s", raw)
	}
	for _, e := range events {
		if e.OnlineTime == "" || e.OfflineTime == "" {
			t.Fatalf("history = %s", raw)
		}
	}
}
