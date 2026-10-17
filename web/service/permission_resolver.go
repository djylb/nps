package service

import "strings"

type PermissionResolver interface {
	NormalizePrincipal(Principal) Principal
	NormalizeIdentity(*SessionIdentity) *SessionIdentity
	KnownRoles() []string
	KnownPermissions() []string
	PermissionCatalog() map[string][]string
}

type StaticPermissionResolver struct{}

func DefaultPermissionResolver() PermissionResolver {
	return StaticPermissionResolver{}
}

func (StaticPermissionResolver) NormalizePrincipal(principal Principal) Principal {
	normalized := principal
	normalized.Kind = strings.TrimSpace(normalized.Kind)
	normalized.SubjectID = strings.TrimSpace(normalized.SubjectID)
	normalized.Username = strings.TrimSpace(normalized.Username)
	normalized.ClientIDs = normalizePrincipalClientIDs(normalized.ClientIDs)
	normalized.Roles = normalizePrincipalStrings(normalized.Roles)
	normalized.Permissions = normalizePrincipalStrings(normalized.Permissions)
	if normalized.Attributes != nil {
		copied := make(map[string]string, len(normalized.Attributes))
		for key, value := range normalized.Attributes {
			copied[strings.TrimSpace(key)] = value
		}
		normalized.Attributes = copied
	}
	if normalized.IsAdmin || containsString(normalized.Roles, RoleAdmin) || permissionSetAllows(normalized.Permissions, PermissionManagementAdmin) {
		normalized.Authenticated = true
		normalized.IsAdmin = true
		normalized.Kind = "admin"
	}
	if !normalized.Authenticated {
		normalized.Authenticated = normalized.IsAdmin ||
			normalized.SubjectID != "" ||
			normalized.Username != "" ||
			len(normalized.ClientIDs) > 0
	}
	if !normalized.Authenticated {
		normalized.Kind = "anonymous"
		normalized.SubjectID = ""
		normalized.Username = ""
		normalized.IsAdmin = false
		normalized.ClientIDs = nil
		normalized.Roles = []string{RoleAnonymous}
		normalized.Permissions = nil
		normalized.Attributes = nil
		return normalized
	}
	if len(normalized.Roles) == 0 {
		if normalized.IsAdmin {
			normalized.Roles = []string{RoleAdmin}
		} else {
			normalized.Roles = []string{RoleUser}
		}
	}
	if normalized.Kind == "" {
		if normalized.IsAdmin {
			normalized.Kind = "admin"
		} else {
			normalized.Kind = "user"
		}
	}
	normalized.Permissions = normalizePermissionSet(normalized.IsAdmin, normalized.Roles, normalized.Permissions)
	return normalized
}

func (r StaticPermissionResolver) NormalizeIdentity(identity *SessionIdentity) *SessionIdentity {
	if identity == nil {
		return nil
	}
	normalized := *identity
	if normalized.Version == 0 {
		normalized.Version = SessionIdentityVersion
	}
	if normalized.Authenticated {
		normalized.ClientIDs = normalizeClientIDs(normalized.ClientIDs)
	}
	if !normalized.Authenticated {
		normalized.Kind = "anonymous"
		normalized.Provider = ""
		normalized.SubjectID = ""
		normalized.Username = ""
		normalized.IsAdmin = false
		normalized.ClientIDs = nil
		normalized.Roles = []string{RoleAnonymous}
		normalized.Permissions = nil
		normalized.Attributes = nil
		return &normalized
	}

	principal := r.NormalizePrincipal(Principal{
		Authenticated: normalized.Authenticated,
		Kind:          normalized.Kind,
		SubjectID:     normalized.SubjectID,
		Username:      normalized.Username,
		IsAdmin:       normalized.IsAdmin,
		ClientIDs:     normalized.ClientIDs,
		Roles:         normalized.Roles,
		Permissions:   normalized.Permissions,
		Attributes:    normalized.Attributes,
	})
	normalized.Authenticated = principal.Authenticated
	normalized.Kind = principal.Kind
	normalized.SubjectID = principal.SubjectID
	normalized.Username = principal.Username
	normalized.IsAdmin = principal.IsAdmin
	normalized.ClientIDs = append([]int(nil), principal.ClientIDs...)
	normalized.Roles = append([]string(nil), principal.Roles...)
	normalized.Permissions = append([]string(nil), principal.Permissions...)
	if len(principal.Attributes) > 0 {
		normalized.Attributes = make(map[string]string, len(principal.Attributes))
		for key, value := range principal.Attributes {
			normalized.Attributes[key] = value
		}
	} else {
		normalized.Attributes = nil
	}
	return &normalized
}

func (StaticPermissionResolver) KnownRoles() []string {
	return []string{RoleAnonymous, RoleUser, RoleAdmin}
}

func (StaticPermissionResolver) KnownPermissions() []string {
	return []string{
		PermissionManagementAdmin,
		PermissionClientsCreate,
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
		PermissionGlobalManage,
	}
}

func (StaticPermissionResolver) PermissionCatalog() map[string][]string {
	return map[string][]string{
		"clients": {
			PermissionClientsCreate,
			PermissionClientsRead,
			PermissionClientsUpdate,
			PermissionClientsDelete,
			PermissionClientsStatus,
		},
		"tunnels": {
			PermissionTunnelsCreate,
			PermissionTunnelsRead,
			PermissionTunnelsUpdate,
			PermissionTunnelsDelete,
			PermissionTunnelsControl,
		},
		"hosts": {
			PermissionHostsCreate,
			PermissionHostsRead,
			PermissionHostsUpdate,
			PermissionHostsDelete,
			PermissionHostsControl,
		},
		"global": {
			PermissionGlobalManage,
		},
	}
}
