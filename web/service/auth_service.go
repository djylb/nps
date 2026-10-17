package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

const (
	SessionIdentityKey     = "session_identity"
	SessionIdentityVersion = 1
)

type AuthService interface {
	Authenticate(AuthenticateInput) (*SessionIdentity, error)
	RegisterUser(RegisterUserInput) (*RegisterUserResult, error)
}

type DefaultAuthService struct {
	Resolver       PermissionResolver
	ConfigProvider func() *servercfg.Snapshot
	Backend        Backend
}

type AuthenticateInput struct {
	Username string
	Password string
	TOTP     string
}

type RegisterUserInput struct {
	Username string
	Password string
}

type RegisterUserResult struct {
	SubjectID string
	Username  string
	ClientIDs []int
}

type SessionIdentity struct {
	Version       int               `json:"version"`
	Kind          string            `json:"kind,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	SubjectID     string            `json:"subject_id,omitempty"`
	Username      string            `json:"username,omitempty"`
	ClientIDs     []int             `json:"client_ids,omitempty"`
	Roles         []string          `json:"roles,omitempty"`
	Permissions   []string          `json:"permissions,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Authenticated bool              `json:"authenticated"`
	IsAdmin       bool              `json:"is_admin"`
}

func (s DefaultAuthService) Authenticate(input AuthenticateInput) (*SessionIdentity, error) {
	cfg := s.config()
	username := strings.TrimSpace(input.Username)

	if identity, ok := authenticateAdmin(username, input.Password, input.TOTP, cfg); ok {
		return s.normalizeIdentity(identity), nil
	}
	if !cfg.Feature.AllowUserLogin || username == "" || input.Password == "" {
		return nil, ErrInvalidCredentials
	}

	var matchedClientIDs []int
	loginMode := "password"
	s.repo().RangeClients(func(client *file.Client) bool {
		if !client.Status || client.NoDisplay {
			return true
		}

		switch {
		case client.WebUserName == "" && client.WebPassword == "":
			if username == "user" && cfg.Feature.AllowUserVkeyLogin && client.Id > 0 && client.VerifyKey == input.Password {
				matchedClientIDs = appendUniqueClientID(matchedClientIDs, client.Id)
				loginMode = "verify_key"
			}
		case client.WebUserName == username:
			if clientCredentialsMatch(client, input.Password, input.TOTP) {
				matchedClientIDs = appendUniqueClientID(matchedClientIDs, client.Id)
			}
		}
		return true
	})

	if len(matchedClientIDs) == 0 {
		return nil, ErrInvalidCredentials
	}
	sort.Ints(matchedClientIDs)

	identity := &SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     buildUserSubjectID(username, matchedClientIDs, loginMode),
		Username:      username,
		ClientIDs:     matchedClientIDs,
		Roles:         []string{RoleUser},
		Attributes: map[string]string{
			"login_mode": loginMode,
		},
	}
	return s.normalizeIdentity(identity), nil
}

func (s DefaultAuthService) RegisterUser(input RegisterUserInput) (*RegisterUserResult, error) {
	cfg := s.config()
	username := strings.TrimSpace(input.Username)
	password := input.Password

	switch {
	case username == "" || strings.TrimSpace(password) == "":
		return nil, ErrInvalidRegistration
	case username == strings.TrimSpace(cfg.Web.Username):
		return nil, ErrReservedUsername
	}

	client := &file.Client{
		Id:          s.repo().NextClientID(),
		Status:      true,
		Cnf:         &file.Config{},
		WebUserName: username,
		WebPassword: password,
		Flow:        &file.Flow{},
	}
	if err := s.repo().CreateClient(client); err != nil {
		return nil, err
	}
	return &RegisterUserResult{
		SubjectID: buildUserSubjectID(username, []int{client.Id}, "password"),
		Username:  username,
		ClientIDs: []int{client.Id},
	}, nil
}

func (identity *SessionIdentity) Normalize() *SessionIdentity {
	return NormalizeSessionIdentityWithResolver(identity, DefaultPermissionResolver())
}

func MarshalSessionIdentity(identity *SessionIdentity) (string, error) {
	return MarshalSessionIdentityWithResolver(identity, DefaultPermissionResolver())
}

func MarshalSessionIdentityWithResolver(identity *SessionIdentity, resolver PermissionResolver) (string, error) {
	if identity == nil {
		return "", nil
	}
	data, err := json.Marshal(NormalizeSessionIdentityWithResolver(identity, resolver))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ParseSessionIdentity(raw string) (*SessionIdentity, error) {
	return ParseSessionIdentityWithResolver(raw, DefaultPermissionResolver())
}

func ParseSessionIdentityWithResolver(raw string, resolver PermissionResolver) (*SessionIdentity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var identity SessionIdentity
	if err := json.Unmarshal([]byte(raw), &identity); err != nil {
		return nil, err
	}
	return NormalizeSessionIdentityWithResolver(&identity, resolver), nil
}

func authenticateAdmin(username, password, totp string, cfg *servercfg.Snapshot) (*SessionIdentity, bool) {
	if identity, ok := AutoAdminIdentity(cfg); ok && strings.TrimSpace(username) == "" && password == "" && strings.TrimSpace(totp) == "" {
		return identity, true
	}
	if username != strings.TrimSpace(cfg.Web.Username) {
		return nil, false
	}
	if !adminCredentialsMatch(password, totp, cfg) {
		return nil, false
	}
	return newAdminIdentity(cfg, "password"), true
}

func adminCredentialsMatch(password, totp string, cfg *servercfg.Snapshot) bool {
	if strings.TrimSpace(cfg.Web.Username) == "" {
		return false
	}
	totpSecret := strings.TrimSpace(cfg.Web.TOTPSecret)
	expectedPassword := cfg.Web.Password
	if totpSecret != "" {
		valid := false
		if totp != "" {
			valid, _ = crypt.ValidateTOTPCode(totpSecret, totp)
		} else {
			passwordLength := len(password)
			if passwordLength >= crypt.TotpLen {
				code := password[passwordLength-crypt.TotpLen:]
				password = password[:passwordLength-crypt.TotpLen]
				valid, _ = crypt.ValidateTOTPCode(totpSecret, code)
			}
		}
		if !valid {
			return false
		}
	}
	return password == expectedPassword
}

func AutoAdminIdentity(cfg *servercfg.Snapshot) (*SessionIdentity, bool) {
	if !adminAutoLoginEnabled(cfg) {
		return nil, false
	}
	return newAdminIdentity(cfg, "auto").Normalize(), true
}

func adminAutoLoginEnabled(cfg *servercfg.Snapshot) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Web.Username) == "" &&
		cfg.Web.Password == "" &&
		strings.TrimSpace(cfg.Web.TOTPSecret) == ""
}

func newAdminIdentity(cfg *servercfg.Snapshot, loginMode string) *SessionIdentity {
	resolvedUsername := "admin"
	if cfg != nil && strings.TrimSpace(cfg.Web.Username) != "" {
		resolvedUsername = strings.TrimSpace(cfg.Web.Username)
	}
	return &SessionIdentity{
		Version:       SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:" + resolvedUsername,
		Username:      resolvedUsername,
		IsAdmin:       true,
		Roles:         []string{RoleAdmin},
		Attributes: map[string]string{
			"login_mode": loginMode,
		},
	}
}

func clientCredentialsMatch(client *file.Client, password, totp string) bool {
	if client == nil {
		return false
	}
	passwordInput := password
	valid := true
	if client.WebTotpSecret != "" {
		valid = false
		if totp != "" {
			valid, _ = crypt.ValidateTOTPCode(client.WebTotpSecret, totp)
		} else {
			passwordLength := len(password)
			if passwordLength >= crypt.TotpLen {
				passwordInput = password[:passwordLength-crypt.TotpLen]
				code := password[passwordLength-crypt.TotpLen:]
				valid, _ = crypt.ValidateTOTPCode(client.WebTotpSecret, code)
			}
		}
	} else if client.WebPassword == "" && client.VerifyKey == password {
		return true
	}
	return valid && client.WebPassword == passwordInput
}

func appendUniqueClientID(clientIDs []int, clientID int) []int {
	if clientID <= 0 {
		return clientIDs
	}
	for _, current := range clientIDs {
		if current == clientID {
			return clientIDs
		}
	}
	return append(clientIDs, clientID)
}

func normalizeClientIDs(clientIDs []int) []int {
	unique := make([]int, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		unique = appendUniqueClientID(unique, clientID)
	}
	return unique
}

func buildUserSubjectID(username string, clientIDs []int, loginMode string) string {
	if strings.TrimSpace(username) != "" && username != "user" {
		return "user:" + strings.TrimSpace(username)
	}
	if len(clientIDs) > 0 {
		return fmt.Sprintf("client:%s:%s", strings.TrimSpace(loginMode), strconv.Itoa(clientIDs[0]))
	}
	return "user"
}

func NormalizeSessionIdentityWithResolver(identity *SessionIdentity, resolver PermissionResolver) *SessionIdentity {
	if resolver == nil {
		resolver = DefaultPermissionResolver()
	}
	return resolver.NormalizeIdentity(identity)
}

func (s DefaultAuthService) normalizeIdentity(identity *SessionIdentity) *SessionIdentity {
	return NormalizeSessionIdentityWithResolver(identity, s.resolver())
}

func (s DefaultAuthService) resolver() PermissionResolver {
	if s.Resolver != nil {
		return s.Resolver
	}
	return DefaultPermissionResolver()
}

func (s DefaultAuthService) config() *servercfg.Snapshot {
	if s.ConfigProvider != nil {
		if cfg := s.ConfigProvider(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}

func (s DefaultAuthService) repo() Repository {
	if s.Backend.Repository != nil {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}
