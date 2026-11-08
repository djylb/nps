package file

import (
	"testing"

	"github.com/djylb/nps/internal/coreproto/coreapi"
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
