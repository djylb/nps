package file

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadJsonFileSupportsAllTypes(t *testing.T) {
	t.Run("clients", func(t *testing.T) {
		input := []byte(`[{"Id":1,"VerifyKey":"v1"},{"Id":2,"VerifyKey":"v2"}]`)
		ids := make([]int, 0)
		keys := make([]string, 0)

		err := loadJsonFile(input, Client{}, func(value interface{}) {
			c := value.(*Client)
			ids = append(ids, c.Id)
			keys = append(keys, c.VerifyKey)
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
			t.Fatalf("unexpected ids: %v", ids)
		}
		if len(keys) != 2 || keys[0] != "v1" || keys[1] != "v2" {
			t.Fatalf("unexpected verify keys: %v", keys)
		}
	})

	t.Run("hosts", func(t *testing.T) {
		input := []byte(`[{"Id":10,"Host":"a.com"},{"Id":11,"Host":"b.com"}]`)
		hosts := make([]string, 0)

		err := loadJsonFile(input, Host{}, func(value interface{}) {
			h := value.(*Host)
			hosts = append(hosts, h.Host)
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(hosts) != 2 || hosts[0] != "a.com" || hosts[1] != "b.com" {
			t.Fatalf("unexpected hosts: %v", hosts)
		}
	})

	t.Run("tunnels", func(t *testing.T) {
		input := []byte(`[{"Id":21,"Mode":"tcp"},{"Id":22,"Mode":"udp"}]`)
		modes := make([]string, 0)

		err := loadJsonFile(input, Tunnel{}, func(value interface{}) {
			tn := value.(*Tunnel)
			modes = append(modes, tn.Mode)
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(modes) != 2 || modes[0] != "tcp" || modes[1] != "udp" {
			t.Fatalf("unexpected modes: %v", modes)
		}
	})
}

func TestLoadJsonFileInvalidJSONReturnsError(t *testing.T) {
	err := loadJsonFile([]byte(`[{"Id":1}`), Client{}, func(value interface{}) {})
	if err == nil {
		t.Fatalf("expected json unmarshal error")
	}
}

func TestCreateEmptyFileCreatesParentAndFile(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "conf", "clients.json")

	if err := createEmptyFile(target); err != nil {
		t.Fatalf("expected createEmptyFile to succeed, got %v", err)
	}
	if err := createEmptyFile(target); err != nil {
		t.Fatalf("expected repeated createEmptyFile to be idempotent, got %v", err)
	}
}

func TestStoreSyncMapToFileSkipsNoStoreEntries(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "clients.json")
	m := &sync.Map{}

	m.Store(1, &Client{Id: 1, VerifyKey: "visible-1", NoStore: false})
	m.Store(2, &Client{Id: 2, VerifyKey: "hidden", NoStore: true})
	m.Store(3, &Client{Id: 3, VerifyKey: "visible-3", NoStore: false})

	storeSyncMapToFile(m, path)

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected stored file to exist, got %v", err)
	}

	var clients []Client
	if err = json.Unmarshal(b, &clients); err != nil {
		t.Fatalf("expected valid json array, got %v", err)
	}

	if len(clients) != 2 {
		t.Fatalf("expected only 2 storable clients, got %d", len(clients))
	}

	found := map[int]bool{}
	for i := range clients {
		found[clients[i].Id] = true
	}
	if !found[1] || !found[3] || found[2] {
		t.Fatalf("unexpected persisted ids: %+v", found)
	}
}
