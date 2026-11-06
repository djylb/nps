package file

import (
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/crypt"
)

func TestVirtualClientsExcludedFromAuthenticationIndex(t *testing.T) {
	db := newTestDb(t)
	db.JsonDb.ClientFilePath = filepath.Join(t.TempDir(), "clients.json")
	key := "virtual-audit-" + t.Name()
	hash := crypt.Blake2b(key)
	t.Cleanup(func() { Blake2bVkeyIndex.Remove(hash) })
	client := &Client{Id: -9, VerifyKey: key, Status: true, Cnf: &Config{}, Flow: &Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatal(err)
	}
	if _, exists := Blake2bVkeyIndex.Get(hash); exists {
		t.Fatal("virtual client indexed on creation")
	}
	if err := db.UpdateClient(client); err != nil {
		t.Fatal(err)
	}
	if _, exists := Blake2bVkeyIndex.Get(hash); exists {
		t.Fatal("virtual client indexed on edit")
	}
	// Even a stale index must not turn a virtual client into an npc identity.
	Blake2bVkeyIndex.Add(hash, -9)
	if _, err := db.GetClientIdByBlake2bVkey(hash); err == nil {
		t.Fatal("virtual stale index accepted")
	}
	if _, err := db.GetClientIdByMd5Vkey(crypt.Md5(key)); err == nil {
		t.Fatal("virtual md5 identity accepted")
	}
	if _, err := db.GetIdByVerifyKey(crypt.Md5(key), "", "", crypt.Md5); err == nil {
		t.Fatal("virtual legacy identity accepted")
	}
	// Updating a virtual record must not remove another real client's index.
	Blake2bVkeyIndex.Add(hash, 7)
	if err := db.UpdateClient(client); err != nil {
		t.Fatal(err)
	}
	if id, ok := Blake2bVkeyIndex.Get(hash); !ok || id != 7 {
		t.Fatal("virtual edit changed real index")
	}
	if id, err := db.GetClientIdByBlake2bVkey(hash); err != nil || id != 7 {
		t.Fatal("positive identity rejected")
	}
}
