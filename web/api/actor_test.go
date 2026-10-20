package api

import (
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

func TestActorCanAccessClient(t *testing.T) {
	actor := UserActor("demo", []int{3, 7})
	if !ActorCanAccessClient(actor, 3) {
		t.Fatal("expected actor to access client 3")
	}
	if !ActorCanAccessClient(actor, 7) {
		t.Fatal("expected actor to access client 7")
	}
	if ActorCanAccessClient(actor, 9) {
		t.Fatal("expected actor to reject client 9")
	}
	if len(actor.Permissions) == 0 {
		t.Fatal("expected default user permissions to be populated")
	}
}

func TestActorPrimaryClientID(t *testing.T) {
	actor := UserActor("demo", []int{0, 5, 8})
	clientID, ok := ActorPrimaryClientID(actor)
	if !ok || clientID != 5 {
		t.Fatalf("ActorPrimaryClientID() = %d, %v, want 5, true", clientID, ok)
	}
}

func TestActorFromSessionIdentity(t *testing.T) {
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7, 7, 2},
		Roles:         []string{webservice.RoleUser},
		Attributes:    map[string]string{"scope": "management"},
	}).Normalize()

	actor := ActorFromSessionIdentity(identity)
	if actor.SubjectID != "user:demo" {
		t.Fatalf("SubjectID = %q, want user:demo", actor.SubjectID)
	}
	if len(actor.ClientIDs) != 2 || actor.ClientIDs[0] != 7 || actor.ClientIDs[1] != 2 {
		t.Fatalf("ClientIDs = %v, want [7 2]", actor.ClientIDs)
	}
	if actor.Attributes["scope"] != "management" {
		t.Fatalf("Attributes = %v, want scope=management", actor.Attributes)
	}
	if len(actor.Permissions) == 0 {
		t.Fatal("expected permissions to round-trip from session identity")
	}
}

func TestAdminActorWithFallback(t *testing.T) {
	actor := AdminActorWithFallback("", "configured-admin")
	if actor.Username != "configured-admin" {
		t.Fatalf("Username = %q, want configured-admin", actor.Username)
	}
	if !actor.IsAdmin || actor.SubjectID != "admin:configured-admin" {
		t.Fatalf("unexpected admin actor: %+v", actor)
	}
}

func TestActorFromSessionIdentityWithFallback(t *testing.T) {
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
	}).Normalize()

	actor := ActorFromSessionIdentityWithFallback(identity, "configured-admin")
	if actor.Username != "configured-admin" {
		t.Fatalf("Username = %q, want configured-admin", actor.Username)
	}
	if actor.SubjectID != "admin:configured-admin" {
		t.Fatalf("SubjectID = %q, want admin:configured-admin", actor.SubjectID)
	}
}

func TestActorVirtualClientAccess(t *testing.T) {
	admin := AdminActorWithFallback("admin", "admin")
	if !ActorCanAccessClient(admin, -1) {
		t.Fatal("admin cannot access virtual client")
	}
	for _, actor := range []*Actor{nil, AnonymousActor(), UserActor("user", []int{1, -1})} {
		if ActorCanAccessClient(actor, -1) {
			t.Fatal("non-admin can access virtual client")
		}
	}
	if ActorCanAccessClient(admin, 0) {
		t.Fatal("zero is not a single client")
	}
}
