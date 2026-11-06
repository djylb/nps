package api

import (
	"errors"
	"strconv"
	"strings"

	webservice "github.com/djylb/nps/web/service"
)

// ApplySessionIdentity stores identity in the session; an error says that
// the session could not be saved.
func ApplySessionIdentity(c Context, identity *webservice.SessionIdentity) error {
	return applySessionIdentity(c, identity)
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

func applySessionIdentity(c Context, identity *webservice.SessionIdentity) error {
	normalized := identity
	if normalized != nil {
		normalized = normalized.Normalize()
	}
	if normalized == nil || !normalized.Authenticated {
		return clearSessionIdentity(c)
	}

	encoded, err := webservice.MarshalSessionIdentity(normalized)
	if err != nil {
		return errors.Join(err, clearSessionIdentity(c))
	}

	errs := []error{
		c.SetSessionValue("auth", true),
		c.SetSessionValue(webservice.SessionIdentityKey, encoded),
		c.SetSessionValue("isAdmin", normalized.IsAdmin),
	}
	if normalized.Username != "" {
		errs = append(errs, c.SetSessionValue("username", normalized.Username))
	} else {
		errs = append(errs, c.DeleteSessionValue("username"))
	}

	if clientID, ok := primaryClientID(normalized.ClientIDs); ok {
		errs = append(errs, c.SetSessionValue("clientId", clientID))
	} else {
		errs = append(errs, c.DeleteSessionValue("clientId"))
	}
	if len(normalized.ClientIDs) > 0 {
		errs = append(errs, c.SetSessionValue("clientIds", joinClientIDs(normalized.ClientIDs)))
	} else {
		errs = append(errs, c.DeleteSessionValue("clientIds"))
	}
	if err := errors.Join(errs...); err != nil {
		c.SetActor(AnonymousActor())
		return err
	}
	c.SetActor(ActorFromSessionIdentity(normalized))
	return nil
}

func clearSessionIdentity(c Context) error {
	err := errors.Join(
		c.SetSessionValue("auth", false),
		c.DeleteSessionValue(webservice.SessionIdentityKey),
		c.DeleteSessionValue("isAdmin"),
		c.DeleteSessionValue("clientId"),
		c.DeleteSessionValue("clientIds"),
		c.DeleteSessionValue("username"),
	)
	c.SetActor(AnonymousActor())
	return err
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
