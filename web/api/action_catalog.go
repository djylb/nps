package api

import (
	"net/http"
	"strings"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type ActionOwnership string

const (
	ActionOwnershipNone   ActionOwnership = ""
	ActionOwnershipClient ActionOwnership = "client"
	ActionOwnershipTunnel ActionOwnership = "tunnel"
	ActionOwnershipHost   ActionOwnership = "host"
)

type ActionSpec struct {
	Controller       string
	Name             string
	Method           string
	APIPath          string
	LegacyPath       string
	Permission       string
	Ownership        ActionOwnership
	RequiredFeatures []PageFeature
	Handler          Handler
	ClientScope      bool
	Protected        bool
}

type ActionEntry struct {
	Controller       string          `json:"controller"`
	Name             string          `json:"name"`
	Method           string          `json:"method"`
	APIPath          string          `json:"api_path"`
	LegacyPath       string          `json:"legacy_path,omitempty"`
	Permission       string          `json:"permission,omitempty"`
	Ownership        ActionOwnership `json:"ownership,omitempty"`
	RequiredFeatures []PageFeature   `json:"required_features,omitempty"`
	ClientScope      bool            `json:"client_scope"`
	Protected        bool            `json:"protected"`
}

func SessionActionCatalog(app *App) []ActionSpec {
	if app == nil {
		return nil
	}
	return []ActionSpec{
		{Controller: "auth", Name: "bootstrap", Method: http.MethodGet, APIPath: "/api/v1/auth/bootstrap", LegacyPath: "/auth/bootstrap", Handler: app.Bootstrap},
		{Controller: "auth", Name: "getauthkey", Method: http.MethodGet, APIPath: "/api/v1/auth/getauthkey", LegacyPath: "/auth/getauthkey", Handler: app.GetAuthKey},
		{Controller: "auth", Name: "gettime", Method: http.MethodGet, APIPath: "/api/v1/auth/gettime", LegacyPath: "/auth/gettime", Handler: app.GetTime},
		{Controller: "auth", Name: "getcert", Method: http.MethodGet, APIPath: "/api/v1/auth/getcert", LegacyPath: "/auth/getcert", Handler: app.GetCert},
		{Controller: "login", Name: "verify", Method: http.MethodPost, APIPath: "/api/v1/login/verify", LegacyPath: "/login/verify", Handler: app.LoginVerify},
		{
			Controller: "login", Name: "register", Method: http.MethodPost, APIPath: "/api/v1/login/register", LegacyPath: "/login/register",
			RequiredFeatures: []PageFeature{PageFeatureAllowUserRegister}, Handler: app.RegisterUser,
		},
		{Controller: "login", Name: "logout", Method: http.MethodPost, APIPath: "/api/v1/login/logout", Handler: app.Logout},
	}
}

func ProtectedActionCatalog(app *App) []ActionSpec {
	if app == nil {
		return nil
	}
	return []ActionSpec{
		{Controller: "live", Name: "status", Method: http.MethodPost, APIPath: "/api/v1/live/status", LegacyPath: "/live/status", Protected: true, Handler: app.LiveStatus},
		{Controller: "index", Name: "stats", Method: http.MethodGet, APIPath: "/api/v1/index/stats", LegacyPath: "/index/stats", Protected: true, Handler: app.DashboardStats},
		{Controller: "index", Name: "updateserver", Method: http.MethodPost, APIPath: "/api/v1/index/updateserver", LegacyPath: "/index/updateserver", Permission: webservice.PermissionManagementAdmin, Protected: true, Handler: app.UpdateServer},
		{Controller: "index", Name: "gettunnel", Method: http.MethodPost, APIPath: "/api/v1/index/gettunnel", LegacyPath: "/index/gettunnel", Permission: webservice.PermissionTunnelsRead, ClientScope: true, Protected: true, Handler: app.GetTunnel},
		{Controller: "index", Name: "add", Method: http.MethodPost, APIPath: "/api/v1/index/add", LegacyPath: "/index/add", Permission: webservice.PermissionTunnelsCreate, ClientScope: true, Protected: true, Handler: app.AddTunnel},
		{Controller: "index", Name: "hostlist", Method: http.MethodPost, APIPath: "/api/v1/index/hostlist", LegacyPath: "/index/hostlist", Permission: webservice.PermissionHostsRead, ClientScope: true, Protected: true, Handler: app.HostList},
		{Controller: "index", Name: "addhost", Method: http.MethodPost, APIPath: "/api/v1/index/addhost", LegacyPath: "/index/addhost", Permission: webservice.PermissionHostsCreate, ClientScope: true, Protected: true, Handler: app.AddHost},
		{Controller: "index", Name: "getonetunnel", Method: http.MethodPost, APIPath: "/api/v1/index/getonetunnel", LegacyPath: "/index/getonetunnel", Permission: webservice.PermissionTunnelsRead, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.GetOneTunnel},
		{Controller: "index", Name: "edit", Method: http.MethodPost, APIPath: "/api/v1/index/edit", LegacyPath: "/index/edit", Permission: webservice.PermissionTunnelsUpdate, ClientScope: true, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.EditTunnel},
		{Controller: "index", Name: "start", Method: http.MethodPost, APIPath: "/api/v1/index/start", LegacyPath: "/index/start", Permission: webservice.PermissionTunnelsControl, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.StartTunnel},
		{Controller: "index", Name: "stop", Method: http.MethodPost, APIPath: "/api/v1/index/stop", LegacyPath: "/index/stop", Permission: webservice.PermissionTunnelsControl, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.StopTunnel},
		{Controller: "index", Name: "clear", Method: http.MethodPost, APIPath: "/api/v1/index/clear", LegacyPath: "/index/clear", Permission: webservice.PermissionTunnelsControl, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.ClearTunnel},
		{Controller: "index", Name: "del", Method: http.MethodPost, APIPath: "/api/v1/index/del", LegacyPath: "/index/del", Permission: webservice.PermissionTunnelsDelete, Ownership: ActionOwnershipTunnel, Protected: true, Handler: app.DeleteTunnel},
		{Controller: "index", Name: "gethost", Method: http.MethodPost, APIPath: "/api/v1/index/gethost", LegacyPath: "/index/gethost", Permission: webservice.PermissionHostsRead, Ownership: ActionOwnershipHost, Protected: true, Handler: app.GetHost},
		{Controller: "index", Name: "edithost", Method: http.MethodPost, APIPath: "/api/v1/index/edithost", LegacyPath: "/index/edithost", Permission: webservice.PermissionHostsUpdate, ClientScope: true, Ownership: ActionOwnershipHost, Protected: true, Handler: app.EditHost},
		{Controller: "index", Name: "starthost", Method: http.MethodPost, APIPath: "/api/v1/index/starthost", LegacyPath: "/index/starthost", Permission: webservice.PermissionHostsControl, Ownership: ActionOwnershipHost, Protected: true, Handler: app.StartHost},
		{Controller: "index", Name: "stophost", Method: http.MethodPost, APIPath: "/api/v1/index/stophost", LegacyPath: "/index/stophost", Permission: webservice.PermissionHostsControl, Ownership: ActionOwnershipHost, Protected: true, Handler: app.StopHost},
		{Controller: "index", Name: "clearhost", Method: http.MethodPost, APIPath: "/api/v1/index/clearhost", LegacyPath: "/index/clearhost", Permission: webservice.PermissionHostsControl, Ownership: ActionOwnershipHost, Protected: true, Handler: app.ClearHost},
		{Controller: "index", Name: "delhost", Method: http.MethodPost, APIPath: "/api/v1/index/delhost", LegacyPath: "/index/delhost", Permission: webservice.PermissionHostsDelete, Ownership: ActionOwnershipHost, Protected: true, Handler: app.DeleteHost},
		{Controller: "client", Name: "list", Method: http.MethodPost, APIPath: "/api/v1/client/list", LegacyPath: "/client/list", Permission: webservice.PermissionClientsRead, Protected: true, Handler: app.ListClients},
		{Controller: "client", Name: "qr", Method: http.MethodGet, APIPath: "/api/v1/client/qr", LegacyPath: "/client/qr", Permission: webservice.PermissionClientsRead, Protected: true, Handler: app.ClientQRCode},
		{Controller: "client", Name: "add", Method: http.MethodPost, APIPath: "/api/v1/client/add", LegacyPath: "/client/add", Permission: webservice.PermissionClientsCreate, Protected: true, Handler: app.AddClient},
		{Controller: "client", Name: "pingclient", Method: http.MethodPost, APIPath: "/api/v1/client/pingclient", LegacyPath: "/client/pingclient", Permission: webservice.PermissionClientsRead, Ownership: ActionOwnershipClient, Protected: true, Handler: app.PingClient},
		{Controller: "client", Name: "getclient", Method: http.MethodPost, APIPath: "/api/v1/client/getclient", LegacyPath: "/client/getclient", Permission: webservice.PermissionClientsRead, Ownership: ActionOwnershipClient, Protected: true, Handler: app.GetClient},
		{Controller: "client", Name: "edit", Method: http.MethodPost, APIPath: "/api/v1/client/edit", LegacyPath: "/client/edit", Permission: webservice.PermissionClientsUpdate, Ownership: ActionOwnershipClient, Protected: true, Handler: app.EditClient},
		{Controller: "client", Name: "clear", Method: http.MethodPost, APIPath: "/api/v1/client/clear", LegacyPath: "/client/clear", Permission: webservice.PermissionManagementAdmin, Protected: true, Handler: app.ClearClient},
		{Controller: "client", Name: "changestatus", Method: http.MethodPost, APIPath: "/api/v1/client/changestatus", LegacyPath: "/client/changestatus", Permission: webservice.PermissionClientsStatus, Ownership: ActionOwnershipClient, Protected: true, Handler: app.ChangeClientStatus},
		{Controller: "client", Name: "update", Method: http.MethodPost, APIPath: "/api/v1/client/update", LegacyPath: "/client/update", Permission: webservice.PermissionManagementAdmin, Ownership: ActionOwnershipClient, Protected: true, Handler: app.UpdateClient},
		{Controller: "client", Name: "del", Method: http.MethodPost, APIPath: "/api/v1/client/del", LegacyPath: "/client/del", Permission: webservice.PermissionClientsDelete, Ownership: ActionOwnershipClient, Protected: true, Handler: app.DeleteClient},
		{Controller: "global", Name: "save", Method: http.MethodPost, APIPath: "/api/v1/global/save", LegacyPath: "/global/save", Permission: webservice.PermissionGlobalManage, Protected: true, Handler: app.SaveGlobal},
		{Controller: "global", Name: "updategeo", Method: http.MethodPost, APIPath: "/api/v1/global/updategeo", LegacyPath: "/global/updategeo", Permission: webservice.PermissionManagementAdmin, Protected: true, Handler: app.UpdateGeoData},
		{Controller: "global", Name: "banlist", Method: http.MethodPost, APIPath: "/api/v1/global/banlist", LegacyPath: "/global/banlist", Permission: webservice.PermissionGlobalManage, Protected: true, Handler: app.BanList},
		{Controller: "global", Name: "unban", Method: http.MethodPost, APIPath: "/api/v1/global/unban", LegacyPath: "/global/unban", Permission: webservice.PermissionGlobalManage, Protected: true, Handler: app.Unban},
		{Controller: "global", Name: "unbanall", Method: http.MethodPost, APIPath: "/api/v1/global/unbanall", LegacyPath: "/global/unbanall", Permission: webservice.PermissionGlobalManage, Protected: true, Handler: app.UnbanAll},
		{Controller: "global", Name: "banclean", Method: http.MethodPost, APIPath: "/api/v1/global/banclean", LegacyPath: "/global/banclean", Permission: webservice.PermissionGlobalManage, Protected: true, Handler: app.BanClean},
	}
}

func VisibleActionEntries(cfg *servercfg.Snapshot, baseURL string, actor *Actor, authz webservice.AuthorizationService, specs []ActionSpec) []ActionEntry {
	if authz == nil {
		authz = webservice.DefaultAuthorizationService{}
	}
	principal := authz.NormalizePrincipal(PrincipalFromActor(actor))
	entries := make([]ActionEntry, 0, len(specs))
	for _, spec := range specs {
		if !actionSpecEnabled(cfg, spec) {
			continue
		}
		if spec.Protected && !principal.Authenticated {
			continue
		}
		if permission := strings.TrimSpace(spec.Permission); permission != "" && !authz.Allows(principal, permission) {
			continue
		}
		entries = append(entries, ActionEntry{
			Controller:       spec.Controller,
			Name:             spec.Name,
			Method:           spec.Method,
			APIPath:          joinBase(baseURL, spec.APIPath),
			LegacyPath:       joinBase(baseURL, spec.LegacyPath),
			Permission:       spec.Permission,
			ClientScope:      spec.ClientScope,
			Ownership:        spec.Ownership,
			Protected:        spec.Protected,
			RequiredFeatures: copyPageFeatures(spec.RequiredFeatures),
		})
	}
	return entries
}

func actionSpecEnabled(cfg *servercfg.Snapshot, spec ActionSpec) bool {
	if cfg == nil {
		cfg = &servercfg.Snapshot{}
	}
	for _, feature := range spec.RequiredFeatures {
		if !pageFeatureEnabled(cfg, feature) {
			return false
		}
	}
	return true
}
