package api

import (
	"encoding/hex"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

func (a *App) Bootstrap(c Context) {
	c.RespondJSON(200, a.bootstrapPayload(c))
}

func (a *App) bootstrapPayload(c Context) BootstrapPayload {
	return a.bootstrapPayloadWithShellAssets(c, a.managementShellAssets(a.currentConfig()))
}

func (a *App) bootstrapPayloadWithShellAssets(c Context, shellAssets ui.ManagementShellAssets) BootstrapPayload {
	cfg := a.currentConfig()
	info := a.system().Info()
	actor := c.Actor()
	if actor == nil {
		actor = AnonymousActor()
	}
	permissions := webservice.DefaultPermissionResolver()
	if a != nil && a.Services.Permissions != nil {
		permissions = a.Services.Permissions
	}
	authz := webservice.AuthorizationService(webservice.DefaultAuthorizationService{})
	if a != nil && a.Services.Authz != nil {
		authz = a.Services.Authz
	}
	nodeID := ""
	if a != nil {
		nodeID = a.NodeID
	}
	identity := currentSessionIdentityWithResolverAndFallback(c, permissions, cfg.Web.Username)
	authenticated := c.SessionValue("auth") == true
	if identity != nil {
		authenticated = identity.Authenticated
	}

	payload := BootstrapPayload{
		Status: 1,
		App: BootstrapApp{
			Name:           cfg.App.Name,
			Version:        info.Version,
			Year:           info.Year,
			WebBaseURL:     cfg.Web.BaseURL,
			HeadCustomCode: cfg.Web.HeadCustomCode,
		},
		Session: BootstrapSession{
			Authenticated: authenticated,
			IsAdmin:       actor.IsAdmin,
			Username:      actor.Username,
			ClientID:      actorPrimaryClientID(actor),
			ClientIDs:     append([]int(nil), actor.ClientIDs...),
			SubjectID:     identityValue(identity, func(v *webservice.SessionIdentity) string { return v.SubjectID }),
			Provider:      identityValue(identity, func(v *webservice.SessionIdentity) string { return v.Provider }),
			Kind:          identityValue(identity, func(v *webservice.SessionIdentity) string { return v.Kind }),
		},
		Actor: actor,
		Pages: VisiblePageEntries(cfg, cfg.Web.BaseURL, actor, authz),
		Actions: append(
			VisibleActionEntries(cfg, cfg.Web.BaseURL, actor, authz, SessionActionCatalog(a)),
			VisibleActionEntries(cfg, cfg.Web.BaseURL, actor, authz, ProtectedActionCatalog(a))...,
		),
		Features: BootstrapFeatures{
			AllowUserLogin:          cfg.Feature.AllowUserLogin,
			AllowUserRegister:       cfg.Feature.AllowUserRegister,
			AllowUserVkeyLogin:      cfg.Feature.AllowUserVkeyLogin,
			AllowFlowLimit:          cfg.Feature.AllowFlowLimit,
			AllowRateLimit:          cfg.Feature.AllowRateLimit,
			AllowTimeLimit:          cfg.Feature.AllowTimeLimit,
			AllowConnectionNumLimit: cfg.Feature.AllowConnectionNumLimit,
			AllowMultiIP:            cfg.Feature.AllowMultiIP,
			AllowTunnelNumLimit:     cfg.Feature.AllowTunnelNumLimit,
			AllowLocalProxy:         cfg.Feature.AllowLocalProxy,
			AllowUserLocal:          cfg.Feature.AllowUserLocal,
			AllowSecretLink:         cfg.Feature.AllowSecretLink,
			AllowUserChangeUsername: cfg.Feature.AllowUserChangeUsername,
			SystemInfoDisplay:       cfg.Feature.SystemInfoDisplay,
			OpenCaptcha:             cfg.Feature.OpenCaptcha,
		},
		Security: BootstrapSecurity{
			SecureMode: cfg.Security.SecureMode,
			ForcePoW:   cfg.Security.ForcePoW,
			PoWBits:    cfg.Security.PoWBits,
		},
		Routes: BootstrapRoutes{
			Page:              joinBase(cfg.Web.BaseURL, "/auth/page"),
			Render:            joinBase(cfg.Web.BaseURL, "/auth/render"),
			AppShell:          joinBase(cfg.Web.BaseURL, "/app"),
			StaticBase:        joinBase(cfg.Web.BaseURL, "/static"),
			AppAssetsBase:     joinBase(cfg.Web.BaseURL, "/static/app"),
			CaptchaNew:        joinBase(cfg.Web.BaseURL, "/captcha/new"),
			CaptchaBase:       joinBase(cfg.Web.BaseURL, "/captcha"),
			APIBase:           joinBase(cfg.Web.BaseURL, "/api/v1"),
			ManagementAPIBase: joinBase(cfg.Web.BaseURL, "/api/v1"),
			APIAuth:           joinBase(cfg.Web.BaseURL, "/api/v1/auth"),
			APILogin:          joinBase(cfg.Web.BaseURL, "/api/v1/login"),
			APICaptcha:        joinBase(cfg.Web.BaseURL, "/api/v1/captcha"),
			APIGetAuthKey:     joinBase(cfg.Web.BaseURL, "/api/v1/auth/getauthkey"),
			APIGetTime:        joinBase(cfg.Web.BaseURL, "/api/v1/auth/gettime"),
			APIGetCert:        joinBase(cfg.Web.BaseURL, "/api/v1/auth/getcert"),
			GetAuthKey:        joinBase(cfg.Web.BaseURL, "/auth/getauthkey"),
			GetTime:           joinBase(cfg.Web.BaseURL, "/auth/gettime"),
			GetCert:           joinBase(cfg.Web.BaseURL, "/auth/getcert"),
			Login:             joinBase(cfg.Web.BaseURL, "/login/index"),
			Logout:            joinBase(cfg.Web.BaseURL, "/login/out"),
		},
		UI: BootstrapUI{
			Mode:                "hybrid",
			ManagementShell:     joinBase(cfg.Web.BaseURL, "/app"),
			LegacyPagesEnabled:  true,
			SPAFallbackEnabled:  true,
			ReactBootstrapReady: true,
			ShellAssetsReady:    shellAssets.Ready,
			ShellAssets:         shellAssets.Clone(),
		},
		Extensions: BootstrapExtensions{
			Authorization: BootstrapAuthorizationExtension{
				Roles:               append([]string(nil), actor.Roles...),
				Permissions:         append([]string(nil), actor.Permissions...),
				ClientIDs:           append([]int(nil), actor.ClientIDs...),
				KnownRoles:          permissions.KnownRoles(),
				KnownPermissions:    permissions.KnownPermissions(),
				ManagementAdmin:     webservice.PermissionManagementAdmin,
				ResourcePermissions: permissions.PermissionCatalog(),
			},
			Cluster: BootstrapClusterExtension{
				NodeID:         nodeID,
				EventsEnabled:  false,
				CallbacksReady: true,
			},
		},
		Request: c.Metadata(),
	}
	if cert, err := crypt.GetRSAPublicKeyPEM(); err == nil {
		payload.PublicKey = cert
	}
	return payload
}

func (a *App) GetAuthKey(c Context) {
	cfg := a.currentConfig()
	response := AuthKeyResponse{Status: 0}
	if cryptKey := cfg.Auth.CryptKey; len(cryptKey) == 16 {
		if b, err := crypt.AesEncrypt([]byte(cfg.Auth.Key), []byte(cryptKey)); err == nil {
			response.Status = 1
			response.CryptAuthKey = hex.EncodeToString(b)
			response.CryptType = "aes cbc"
		}
	}
	c.RespondJSON(200, response)
}

func (a *App) GetTime(c Context) {
	c.RespondJSON(200, TimeResponse{Time: common.TimeNow().Unix()})
}

func (a *App) GetCert(c Context) {
	response := CertResponse{Status: 0}
	if cert, err := crypt.GetRSAPublicKeyPEM(); err == nil {
		response.Status = 1
		response.Cert = cert
	}
	c.RespondJSON(200, response)
}

func (a *App) Logout(c Context) {
	a.Emit(c, Event{
		Name:     "session.logout",
		Resource: "session",
		Action:   "logout",
	})
	_ = clearSessionIdentity(c)
	_ = c.DeleteSessionValue("login_nonce")
	ajax(c, "logout success", 1)
}

func actorPrimaryClientID(actor *Actor) *int {
	if actor == nil {
		return nil
	}
	if clientID, ok := ActorPrimaryClientID(actor); ok {
		return &clientID
	}
	return nil
}

func joinBase(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return suffix
	}
	return base + suffix
}

func identityValue(identity *webservice.SessionIdentity, getter func(*webservice.SessionIdentity) string) string {
	if identity == nil {
		return ""
	}
	return getter(identity)
}
