package bridge

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/file"
)

func TestRegistryConnectionHistory(t *testing.T) {
	db := file.New(t.TempDir())
	const id = 987654
	c := &model.Client{Id: id, ConnectionHistory: new(model.ConnectionHistory)}
	db.JsonDb.Clients.Store(id, c)
	b := NewTunnel(db, &sync.Map{}, Options{CoreEntry: stubEntry{}, DisconnectTime: 30})
	first := NewClient(id, NewNode("one", "", 8), Primary)
	b.loadOrStoreClient(id, first)
	b.loadOrStoreClient(id, NewClient(id, NewNode("two", "", 8), Primary))
	b.DelClient(id)
	b.DelClient(id)
	b.loadOrStoreClient(id, NewClient(id, NewNode("three", "", 8), Primary))
	b.DelClient(id)
	raw, err := json.Marshal(c.ConnectionHistory)
	if err != nil {
		t.Fatal(err)
	}
	var events []model.ConnectionEvent
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
