package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
)

// A reload of public_vkey or visitor_vkey swaps the key clients.
func TestReplaceKeyClients(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := file.New(root)
	srv := New(db, Config{CoreEntry: realEntry(t, db), DisconnectTimeout: 30})
	keys := func() map[string]model.KeyKind {
		m := map[string]model.KeyKind{}
		db.JsonDb.Clients.Range(func(_, v any) bool {
			if c := v.(*model.Client); c.KeyKind != model.KeyControl {
				m[c.VerifyKey] = c.KeyKind
			}
			return true
		})
		return m
	}
	srv.addKeyClients("pub", "vis")
	srv.replaceKeyClients("", "vis2")
	if got := keys(); len(got) != 1 || got["vis2"] != model.KeyVisitor {
		t.Fatalf("key clients %v", got)
	}
	if id, err := db.GetClientIdByBlake2bVkey(crypt.Blake2b("vis")); err == nil {
		t.Fatalf("removed key still resolves to %d", id)
	}
}
