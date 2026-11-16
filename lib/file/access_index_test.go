package file

import (
	"testing"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/model"
)

func TestAccessIndexFollowsPassword(t *testing.T) {
	p := newTaskPasswords()
	task := &model.Tunnel{Id: 1, Mode: "secret", Password: "one"}
	if err := p.reserve(task, true); err != nil {
		t.Fatal(err)
	}
	id := coreapi.AccessID([]byte("one"))
	if got, ok := p.lookupAccess(id); !ok || got != 1 {
		t.Fatal(got, ok)
	}
	task.Password = "two"
	if err := p.reserve(task, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.lookupAccess(id); ok {
		t.Fatal("old password still finds the task")
	}
	p.release(1)
	if _, ok := p.lookupAccess(coreapi.AccessID([]byte("two"))); ok {
		t.Fatal("released password still finds the task")
	}
}

// npc before 0.35 names a point-to-point task by the MD5 hex digest of its
// password; other modes and other passwords find nothing.
func TestGetTaskByLegacySecret(t *testing.T) {
	db := New(t.TempDir())
	db.JsonDb.Tasks.Store(1, &model.Tunnel{Id: 1, Mode: "secret", Password: "pw"})
	db.JsonDb.Tasks.Store(2, &model.Tunnel{Id: 2, Mode: "tcp", Password: "tcp-pw"})
	if got := db.GetTaskByLegacySecret([]byte(crypt.Md5("pw"))); got == nil || got.Id != 1 {
		t.Fatalf("got %v", got)
	}
	for _, pw := range []string{"tcp-pw", "other", ""} {
		if got := db.GetTaskByLegacySecret([]byte(crypt.Md5(pw))); got != nil {
			t.Fatalf("%q found task %d", pw, got.Id)
		}
	}
}
