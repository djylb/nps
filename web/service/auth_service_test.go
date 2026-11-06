package service

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestSessionIdentityRoundTrip(t *testing.T) {
	raw, err := MarshalSessionIdentity(&SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{9, 0, 9, 3},
		Roles:         []string{RoleUser},
	})
	if err != nil {
		t.Fatalf("MarshalSessionIdentity() error = %v", err)
	}

	identity, err := ParseSessionIdentity(raw)
	if err != nil {
		t.Fatalf("ParseSessionIdentity() error = %v", err)
	}
	if identity == nil {
		t.Fatal("ParseSessionIdentity() returned nil identity")
	}
	if identity.Version != SessionIdentityVersion {
		t.Fatalf("Version = %d, want %d", identity.Version, SessionIdentityVersion)
	}
	if got, want := len(identity.ClientIDs), 2; got != want {
		t.Fatalf("len(ClientIDs) = %d, want %d", got, want)
	}
	if identity.ClientIDs[0] != 9 || identity.ClientIDs[1] != 3 {
		t.Fatalf("ClientIDs = %v, want [9 3]", identity.ClientIDs)
	}
	if len(identity.Permissions) == 0 {
		t.Fatal("Permissions should be derived from roles")
	}
	found := false
	for _, permission := range identity.Permissions {
		if permission == PermissionClientsRead {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Permissions = %v, expected %q to be present", identity.Permissions, PermissionClientsRead)
	}
}

func TestParseSessionIdentityWithResolver(t *testing.T) {
	raw := `{"version":1,"authenticated":true,"kind":"service","subject_id":"service:demo","roles":["service"]}`
	identity, err := ParseSessionIdentityWithResolver(raw, stubPermissionResolver{
		normalizeIdentity: func(identity *SessionIdentity) *SessionIdentity {
			if identity == nil {
				return nil
			}
			normalized := *identity
			normalized.Permissions = []string{"service:access"}
			return &normalized
		},
	})
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if identity == nil {
		t.Fatal("ParseSessionIdentityWithResolver() returned nil identity")
	}
	if len(identity.Permissions) != 1 || identity.Permissions[0] != "service:access" {
		t.Fatalf("Permissions = %v, want [service:access]", identity.Permissions)
	}
}

func TestDefaultAuthServiceAuthenticateUsesConfigProvider(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{
					Username: "admin-from-provider",
					Password: "provider-secret",
				},
			}
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "admin-from-provider",
		Password: "provider-secret",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil {
		t.Fatal("Authenticate() returned nil identity")
	}
	if !identity.IsAdmin || identity.Username != "admin-from-provider" {
		t.Fatalf("Authenticate() returned unexpected identity: %+v", identity)
	}
}

func TestAutoAdminIdentity(t *testing.T) {
	identity, ok := AutoAdminIdentity(&servercfg.Snapshot{
		Web: servercfg.WebConfig{},
	})
	if !ok {
		t.Fatal("AutoAdminIdentity() should enable implicit admin login when username and password are empty")
	}
	if identity == nil {
		t.Fatal("AutoAdminIdentity() returned nil identity")
	}
	if !identity.Authenticated || !identity.IsAdmin {
		t.Fatalf("AutoAdminIdentity() returned unexpected identity: %+v", identity)
	}
	if identity.Username != "admin" {
		t.Fatalf("AutoAdminIdentity() username = %q, want admin", identity.Username)
	}
	if identity.Attributes["login_mode"] != "auto" {
		t.Fatalf("AutoAdminIdentity() login mode = %q, want auto", identity.Attributes["login_mode"])
	}

	if disabled, ok := AutoAdminIdentity(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			TOTPSecret: "totp-secret",
		},
	}); ok || disabled != nil {
		t.Fatalf("AutoAdminIdentity() should stay disabled when TOTP is configured, got %+v, %v", disabled, ok)
	}
}
