package service

import "testing"

type stubPermissionResolver struct {
	normalizePrincipal func(Principal) Principal
	normalizeIdentity  func(*SessionIdentity) *SessionIdentity
}

func (s stubPermissionResolver) NormalizePrincipal(principal Principal) Principal {
	if s.normalizePrincipal != nil {
		return s.normalizePrincipal(principal)
	}
	return principal
}

func (s stubPermissionResolver) NormalizeIdentity(identity *SessionIdentity) *SessionIdentity {
	if s.normalizeIdentity != nil {
		return s.normalizeIdentity(identity)
	}
	return identity
}

func (stubPermissionResolver) KnownRoles() []string {
	return []string{"stub"}
}

func (stubPermissionResolver) KnownPermissions() []string {
	return []string{"stub:access"}
}

func (stubPermissionResolver) PermissionCatalog() map[string][]string {
	return map[string][]string{"stub": {"stub:access"}}
}

func TestNormalizePrincipalDerivesDefaultPermissions(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := authz.NormalizePrincipal(Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7, 7, 2},
		Roles:         []string{RoleUser},
	})

	if len(principal.ClientIDs) != 2 || principal.ClientIDs[0] != 7 || principal.ClientIDs[1] != 2 {
		t.Fatalf("ClientIDs = %v, want [7 2]", principal.ClientIDs)
	}
	if !authz.Allows(principal, PermissionClientsRead) {
		t.Fatalf("user principal should allow %q", PermissionClientsRead)
	}
	if authz.Allows(principal, PermissionManagementAdmin) {
		t.Fatalf("user principal should not allow %q", PermissionManagementAdmin)
	}
}

func TestRequireAdminAcceptsExplicitManagementPermission(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "service",
		SubjectID:     "service:console",
		Permissions:   []string{PermissionManagementAdmin},
	}

	if err := authz.RequireAdmin(principal); err != nil {
		t.Fatalf("RequireAdmin() error = %v, want nil", err)
	}
}

func TestAllowsSupportsWildcardPermissions(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "service",
		SubjectID:     "service:automation",
		Roles:         []string{"service"},
		Permissions:   []string{"clients:*"},
	}

	if !authz.Allows(principal, PermissionClientsDelete) {
		t.Fatalf("wildcard permission should allow %q", PermissionClientsDelete)
	}
	if authz.Allows(principal, PermissionHostsRead) {
		t.Fatalf("clients wildcard should not allow %q", PermissionHostsRead)
	}
}

func TestAuthorizationServiceUsesInjectedResolver(t *testing.T) {
	authz := DefaultAuthorizationService{
		Resolver: stubPermissionResolver{
			normalizePrincipal: func(principal Principal) Principal {
				principal.Authenticated = true
				principal.Permissions = []string{"stub:access"}
				return principal
			},
		},
	}

	if err := authz.RequirePermission(Principal{}, "stub:access"); err != nil {
		t.Fatalf("RequirePermission() error = %v, want nil", err)
	}
	if err := authz.RequirePermission(Principal{}, PermissionClientsRead); err == nil {
		t.Fatal("RequirePermission() should reject permissions not granted by injected resolver")
	}
}
