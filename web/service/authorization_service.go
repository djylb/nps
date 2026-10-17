package service

import (
	"strings"
)

const (
	RoleAnonymous = "anonymous"
	RoleAdmin     = "admin"
	RoleUser      = "user"
)

const (
	PermissionAll             = "*"
	PermissionManagementAdmin = "management:admin"
	PermissionClientsCreate   = "clients:create"
	PermissionClientsRead     = "clients:read"
	PermissionClientsUpdate   = "clients:update"
	PermissionClientsDelete   = "clients:delete"
	PermissionClientsStatus   = "clients:status"
	PermissionTunnelsCreate   = "tunnels:create"
	PermissionTunnelsRead     = "tunnels:read"
	PermissionTunnelsUpdate   = "tunnels:update"
	PermissionTunnelsDelete   = "tunnels:delete"
	PermissionTunnelsControl  = "tunnels:control"
	PermissionHostsCreate     = "hosts:create"
	PermissionHostsRead       = "hosts:read"
	PermissionHostsUpdate     = "hosts:update"
	PermissionHostsDelete     = "hosts:delete"
	PermissionHostsControl    = "hosts:control"
	PermissionGlobalManage    = "global:manage"
)

type AuthorizationService interface {
	NormalizePrincipal(Principal) Principal
	ClientScope(Principal) ClientScope
	Permissions(Principal) []string
	Allows(Principal, string) bool
	ResolveClient(Principal, int) (int, error)
	RequirePermission(Principal, string) error
	RequireAdmin(Principal) error
	RequireClient(Principal, int) error
	RequireTunnel(Principal, int) error
	RequireHost(Principal, int) error
}

type DefaultAuthorizationService struct {
	Resolver PermissionResolver
	Backend  Backend
}

type Principal struct {
	Kind          string
	SubjectID     string
	Username      string
	ClientIDs     []int
	Roles         []string
	Permissions   []string
	Attributes    map[string]string
	Authenticated bool
	IsAdmin       bool
}

type ClientScope struct {
	All             bool
	PrimaryClientID int
	ClientIDs       []int
}

func (s DefaultAuthorizationService) NormalizePrincipal(principal Principal) Principal {
	return s.resolver().NormalizePrincipal(principal)
}

func (s DefaultAuthorizationService) ClientScope(principal Principal) ClientScope {
	normalized := s.NormalizePrincipal(principal)
	scope := ClientScope{
		All:       s.Allows(normalized, PermissionManagementAdmin),
		ClientIDs: append([]int(nil), normalized.ClientIDs...),
	}
	for _, clientID := range normalized.ClientIDs {
		if clientID > 0 {
			scope.PrimaryClientID = clientID
			break
		}
	}
	return scope
}

func (s DefaultAuthorizationService) Permissions(principal Principal) []string {
	normalized := s.NormalizePrincipal(principal)
	return append([]string(nil), normalized.Permissions...)
}

func (s DefaultAuthorizationService) Allows(principal Principal, permission string) bool {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return false
	}
	return permissionSetAllows(normalized.Permissions, permission)
}

func (s DefaultAuthorizationService) ResolveClient(principal Principal, requestedClientID int) (int, error) {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return 0, ErrUnauthenticated
	}
	if s.Allows(normalized, PermissionManagementAdmin) {
		return requestedClientID, nil
	}
	if requestedClientID > 0 {
		if s.canAccessClient(normalized, requestedClientID) {
			return requestedClientID, nil
		}
		return 0, ErrForbidden
	}
	scope := s.ClientScope(normalized)
	if scope.PrimaryClientID > 0 {
		return scope.PrimaryClientID, nil
	}
	return 0, ErrForbidden
}

func (s DefaultAuthorizationService) RequirePermission(principal Principal, permission string) error {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return ErrUnauthenticated
	}
	if s.Allows(normalized, permission) {
		return nil
	}
	return ErrForbidden
}

func (s DefaultAuthorizationService) RequireAdmin(principal Principal) error {
	return s.RequirePermission(principal, PermissionManagementAdmin)
}

func (s DefaultAuthorizationService) RequireClient(principal Principal, clientID int) error {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return ErrUnauthenticated
	}
	if s.canAccessClient(normalized, clientID) {
		return nil
	}
	return ErrForbidden
}

func (s DefaultAuthorizationService) RequireTunnel(principal Principal, tunnelID int) error {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return ErrUnauthenticated
	}
	if s.Allows(normalized, PermissionManagementAdmin) {
		return nil
	}
	for _, clientID := range normalized.ClientIDs {
		if s.repo().ClientOwnsTunnel(clientID, tunnelID) {
			return nil
		}
	}
	return ErrForbidden
}

func (s DefaultAuthorizationService) RequireHost(principal Principal, hostID int) error {
	normalized := s.NormalizePrincipal(principal)
	if !normalized.Authenticated {
		return ErrUnauthenticated
	}
	if s.Allows(normalized, PermissionManagementAdmin) {
		return nil
	}
	for _, clientID := range normalized.ClientIDs {
		if s.repo().ClientOwnsHost(clientID, hostID) {
			return nil
		}
	}
	return ErrForbidden
}

func (s DefaultAuthorizationService) canAccessClient(principal Principal, clientID int) bool {
	if clientID <= 0 {
		return false
	}
	if s.Allows(principal, PermissionManagementAdmin) {
		return true
	}
	for _, current := range principal.ClientIDs {
		if current == clientID {
			return true
		}
	}
	return false
}

func normalizePrincipalClientIDs(clientIDs []int) []int {
	normalized := make([]int, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID <= 0 {
			continue
		}
		exists := false
		for _, current := range normalized {
			if current == clientID {
				exists = true
				break
			}
		}
		if !exists {
			normalized = append(normalized, clientID)
		}
	}
	return normalized
}

func normalizePrincipalStrings(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || containsString(normalized, value) {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func normalizePermissionSet(isAdmin bool, roles []string, explicit []string) []string {
	permissions := normalizePrincipalStrings(explicit)
	for _, role := range roles {
		for _, permission := range DefaultRolePermissions(role) {
			if !containsString(permissions, permission) {
				permissions = append(permissions, permission)
			}
		}
	}
	if isAdmin && !containsString(permissions, PermissionAll) {
		permissions = append(permissions, PermissionAll)
	}
	return permissions
}

func DefaultRolePermissions(role string) []string {
	switch strings.TrimSpace(role) {
	case RoleAdmin:
		return []string{PermissionAll, PermissionManagementAdmin, PermissionGlobalManage}
	case RoleUser:
		return []string{
			PermissionClientsRead,
			PermissionClientsUpdate,
			PermissionClientsDelete,
			PermissionClientsStatus,
			PermissionTunnelsCreate,
			PermissionTunnelsRead,
			PermissionTunnelsUpdate,
			PermissionTunnelsDelete,
			PermissionTunnelsControl,
			PermissionHostsCreate,
			PermissionHostsRead,
			PermissionHostsUpdate,
			PermissionHostsDelete,
			PermissionHostsControl,
		}
	default:
		return nil
	}
}

func permissionSetAllows(granted []string, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return true
	}
	for _, permission := range granted {
		if matchPermission(permission, required) {
			return true
		}
	}
	return false
}

func matchPermission(granted, required string) bool {
	granted = strings.TrimSpace(granted)
	if granted == "" {
		return false
	}
	if granted == PermissionAll || granted == required {
		return true
	}
	if strings.HasSuffix(granted, "*") {
		prefix := strings.TrimSuffix(granted, "*")
		return strings.HasPrefix(required, prefix)
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s DefaultAuthorizationService) resolver() PermissionResolver {
	if s.Resolver != nil {
		return s.Resolver
	}
	return DefaultPermissionResolver()
}

func (s DefaultAuthorizationService) repo() Repository {
	if s.Backend.Repository != nil {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}
