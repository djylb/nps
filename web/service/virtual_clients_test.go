package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestVirtualClientAuthorization(t *testing.T) {
	authz := DefaultAuthorizationService{}
	admin := Principal{Authenticated: true, IsAdmin: true, Kind: "admin"}
	user := Principal{Authenticated: true, Kind: "user", ClientIDs: []int{1}}
	for _, id := range []int{-1, -9, 0, 1} {
		if err := authz.RequireClient(admin, id); err != nil {
			t.Fatalf("admin id=%d: %v", id, err)
		}
	}
	for _, id := range []int{-1, -9, 0, 2} {
		if err := authz.RequireClient(user, id); !errors.Is(err, ErrForbidden) {
			t.Fatalf("user id=%d allowed", id)
		}
	}
	if authz.RequireClient(user, 1) != nil {
		t.Fatal("ordinary owner denied")
	}
	if !errors.Is(authz.RequireClient(Principal{}, -1), ErrUnauthenticated) {
		t.Fatal("anonymous virtual access allowed")
	}
}

type virtualClientRepo struct {
	stubRepository
	deleted, updatedIndex int
	client                *file.Client
}

func (r *virtualClientRepo) DeleteClient(int) error                     { r.deleted++; return nil }
func (r *virtualClientRepo) ReplaceClientVKeyIndex(string, string, int) { r.updatedIndex++ }
func (r *virtualClientRepo) GetClient(int) (*file.Client, error)        { return r.client, nil }

type virtualClientRuntime struct {
	stubRuntime
	updateCalls  int
	disconnected []int
}

func (r *virtualClientRuntime) RequestClientUpdate(int) (int, error) { r.updateCalls++; return 1, nil }
func (r *virtualClientRuntime) DisconnectClient(id int)              { r.disconnected = append(r.disconnected, id) }

func TestVirtualClientManagementAndRemoteGuards(t *testing.T) {
	client := &file.Client{Id: -1, Status: true, VerifyKey: "virtual-key", Cnf: &file.Config{}, Flow: &file.Flow{}}
	repo := &virtualClientRepo{client: client}
	runtime := &virtualClientRuntime{}
	svc := DefaultClientService{Backend: Backend{Repository: repo, Runtime: runtime}}
	if _, err := svc.Get(-1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Edit(EditClientInput{ID: -1, IsAdmin: true, VKey: "virtual-key", Remark: "updated", RateLimit: 128, MaxConn: 5}); err != nil {
		t.Fatal(err)
	}
	if client.Remark != "updated" || client.MaxConn != 5 || client.RateLimit != 128 || repo.updatedIndex != 0 {
		t.Fatalf("invalid virtual edit: %+v index=%d", client, repo.updatedIndex)
	}
	if err := svc.ChangeStatus(-1, false); err != nil {
		t.Fatal(err)
	}
	if client.Status || len(runtime.disconnected) != 1 || runtime.disconnected[0] != -1 {
		t.Fatal("virtual disable failed")
	}
	if err := svc.ChangeStatus(-1, true); err != nil || !client.Status {
		t.Fatal("virtual enable failed")
	}
	for _, id := range []int{-1, 0} {
		if !errors.Is(svc.Delete(id), ErrVirtualClientOperation) {
			t.Fatal("virtual/sentinel deletion allowed")
		}
		if _, err := svc.RequestUpdate(id); !errors.Is(err, ErrVirtualClientOperation) {
			t.Fatal("virtual/sentinel remote update allowed")
		}
	}
	if repo.deleted != 0 || runtime.updateCalls != 0 {
		t.Fatal("blocked operation reached backend")
	}
}
