package api

import (
	"strconv"
	"strings"

	webservice "github.com/djylb/nps/web/service"
)

func ApplySessionIdentity(c Context, identity *webservice.SessionIdentity) {
	applySessionIdentity(c, identity)
}

func currentSessionIdentityWithResolverAndFallback(c Context, resolver webservice.PermissionResolver, fallbackAdminUsername string) *webservice.SessionIdentity {
	raw, _ := c.SessionValue(webservice.SessionIdentityKey).(string)
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, resolver)
	if err == nil && identity != nil {
		return identity
	}
	if c.SessionValue("auth") != true {
		return nil
	}
	if isAdmin, _ := c.SessionValue("isAdmin").(bool); isAdmin {
		username, _ := c.SessionValue("username").(string)
		if strings.TrimSpace(username) == "" {
			username = strings.TrimSpace(fallbackAdminUsername)
		}
		return (&webservice.SessionIdentity{
			Version:       webservice.SessionIdentityVersion,
			Authenticated: true,
			Kind:          "admin",
			Provider:      "legacy",
			SubjectID:     "admin:" + strings.TrimSpace(username),
			Username:      strings.TrimSpace(username),
			IsAdmin:       true,
			Roles:         []string{"admin"},
			Permissions:   []string{"*"},
		}).Normalize()
	}
	username, _ := c.SessionValue("username").(string)
	return (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "legacy",
		SubjectID:     legacyUserSubject(strings.TrimSpace(username), sessionClientIDsFromContext(c)),
		Username:      strings.TrimSpace(username),
		ClientIDs:     sessionClientIDsFromContext(c),
		Roles:         []string{"user"},
	}).Normalize()
}

func applySessionIdentity(c Context, identity *webservice.SessionIdentity) {
	normalized := identity
	if normalized != nil {
		normalized = normalized.Normalize()
	}
	if normalized == nil || !normalized.Authenticated {
		clearSessionIdentity(c)
		return
	}

	encoded, err := webservice.MarshalSessionIdentity(normalized)
	if err != nil {
		clearSessionIdentity(c)
		return
	}

	c.SetSessionValue("auth", true)
	c.SetSessionValue(webservice.SessionIdentityKey, encoded)
	c.SetSessionValue("isAdmin", normalized.IsAdmin)
	if normalized.Username != "" {
		c.SetSessionValue("username", normalized.Username)
	} else {
		c.DeleteSessionValue("username")
	}

	if clientID, ok := primaryClientID(normalized.ClientIDs); ok {
		c.SetSessionValue("clientId", clientID)
	} else {
		c.DeleteSessionValue("clientId")
	}
	if len(normalized.ClientIDs) > 0 {
		c.SetSessionValue("clientIds", joinClientIDs(normalized.ClientIDs))
	} else {
		c.DeleteSessionValue("clientIds")
	}
	c.SetActor(ActorFromSessionIdentity(normalized))
}

func clearSessionIdentity(c Context) {
	c.SetSessionValue("auth", false)
	c.DeleteSessionValue(webservice.SessionIdentityKey)
	c.DeleteSessionValue("isAdmin")
	c.DeleteSessionValue("clientId")
	c.DeleteSessionValue("clientIds")
	c.DeleteSessionValue("username")
	c.SetActor(AnonymousActor())
}

func joinClientIDs(clientIDs []int) string {
	parts := make([]string, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID > 0 {
			parts = append(parts, strconv.Itoa(clientID))
		}
	}
	return strings.Join(parts, ",")
}

func primaryClientID(clientIDs []int) (int, bool) {
	for _, clientID := range clientIDs {
		if clientID > 0 {
			return clientID, true
		}
	}
	return 0, false
}

func sessionClientIDsFromContext(c Context) []int {
	if raw, ok := c.SessionValue("clientIds").(string); ok {
		return parseClientIDs(raw)
	}
	if clientID, ok := c.SessionValue("clientId").(int); ok && clientID > 0 {
		return []int{clientID}
	}
	return nil
}

func parseClientIDs(raw string) []int {
	parts := strings.Split(raw, ",")
	clientIDs := make([]int, 0, len(parts))
	for _, part := range parts {
		clientID, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && clientID > 0 {
			clientIDs = append(clientIDs, clientID)
		}
	}
	return clientIDs
}

func legacyUserSubject(username string, clientIDs []int) string {
	if username != "" && username != "user" {
		return "user:" + username
	}
	if clientID, ok := primaryClientID(clientIDs); ok {
		return "client:legacy:" + strconv.Itoa(clientID)
	}
	return "user"
}
