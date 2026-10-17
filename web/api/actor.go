package api

import (
	"strings"

	webservice "github.com/djylb/nps/web/service"
)

type Actor struct {
	Kind        string            `json:"kind"`
	SubjectID   string            `json:"subject_id,omitempty"`
	Username    string            `json:"username,omitempty"`
	IsAdmin     bool              `json:"is_admin"`
	ClientIDs   []int             `json:"client_ids,omitempty"`
	Roles       []string          `json:"roles,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

func AnonymousActor() *Actor {
	return &Actor{
		Kind:  webservice.RoleAnonymous,
		Roles: []string{webservice.RoleAnonymous},
	}
}

func AdminActorWithFallback(username, fallbackAdminUsername string) *Actor {
	username = strings.TrimSpace(username)
	if username == "" {
		username = strings.TrimSpace(fallbackAdminUsername)
	}
	if username == "" {
		username = "admin"
	}
	principal := webservice.DefaultAuthorizationService{}.NormalizePrincipal(webservice.Principal{
		Authenticated: true,
		Kind:          "admin",
		SubjectID:     "admin:" + username,
		Username:      username,
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
		Permissions:   []string{webservice.PermissionAll},
	})
	return &Actor{
		Kind:        principal.Kind,
		SubjectID:   principal.SubjectID,
		Username:    principal.Username,
		IsAdmin:     principal.IsAdmin,
		ClientIDs:   append([]int(nil), principal.ClientIDs...),
		Roles:       append([]string(nil), principal.Roles...),
		Permissions: append([]string(nil), principal.Permissions...),
	}
}

func UserActor(username string, clientIDs []int) *Actor {
	filtered := make([]int, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID > 0 {
			filtered = append(filtered, clientID)
		}
	}
	principal := webservice.DefaultAuthorizationService{}.NormalizePrincipal(webservice.Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     strings.TrimSpace(username),
		Username:      strings.TrimSpace(username),
		ClientIDs:     filtered,
		Roles:         []string{webservice.RoleUser},
	})
	return &Actor{
		Kind:        principal.Kind,
		SubjectID:   principal.SubjectID,
		Username:    principal.Username,
		IsAdmin:     principal.IsAdmin,
		ClientIDs:   append([]int(nil), principal.ClientIDs...),
		Roles:       append([]string(nil), principal.Roles...),
		Permissions: append([]string(nil), principal.Permissions...),
	}
}

func ActorFromSessionIdentity(identity *webservice.SessionIdentity) *Actor {
	return ActorFromSessionIdentityWithFallback(identity, "")
}

func ActorFromSessionIdentityWithFallback(identity *webservice.SessionIdentity, fallbackAdminUsername string) *Actor {
	if identity == nil || !identity.Authenticated {
		return AnonymousActor()
	}
	actor := &Actor{
		Kind:        strings.TrimSpace(identity.Kind),
		SubjectID:   strings.TrimSpace(identity.SubjectID),
		Username:    strings.TrimSpace(identity.Username),
		IsAdmin:     identity.IsAdmin,
		ClientIDs:   append([]int(nil), identity.ClientIDs...),
		Roles:       append([]string(nil), identity.Roles...),
		Permissions: append([]string(nil), identity.Permissions...),
	}
	if len(identity.Attributes) > 0 {
		actor.Attributes = make(map[string]string, len(identity.Attributes))
		for key, value := range identity.Attributes {
			actor.Attributes[key] = value
		}
	}
	if actor.Kind == "" {
		if actor.IsAdmin {
			actor.Kind = "admin"
		} else {
			actor.Kind = "user"
		}
	}
	if actor.Kind == "admin" && actor.SubjectID == "" {
		if actor.Username == "" {
			return AdminActorWithFallback("", fallbackAdminUsername)
		}
		return AdminActorWithFallback(actor.Username, fallbackAdminUsername)
	}
	return actor
}

func ActorCanAccessClient(actor *Actor, clientID int) bool {
	if actor == nil || clientID <= 0 {
		return false
	}
	if actor.IsAdmin {
		return true
	}
	for _, current := range actor.ClientIDs {
		if current == clientID {
			return true
		}
	}
	return false
}

func ActorPrimaryClientID(actor *Actor) (int, bool) {
	if actor == nil {
		return 0, false
	}
	for _, clientID := range actor.ClientIDs {
		if clientID > 0 {
			return clientID, true
		}
	}
	return 0, false
}
