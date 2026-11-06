package api

import (
	"strings"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type PageOwnership string

const (
	PageOwnershipClient PageOwnership = "client"
	PageOwnershipTunnel PageOwnership = "tunnel"
	PageOwnershipHost   PageOwnership = "host"
)

type PageFeature string

const (
	PageFeatureAllowUserRegister PageFeature = "allow_user_register"
)

type PageParam struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
}

type PageSpec struct {
	Controller            string
	Action                string
	Permission            string
	Ownership             PageOwnership
	Section               string
	Menu                  string
	Template              string
	Params                []PageParam
	RequiredFeatures      []PageFeature
	IncludeControllerRoot bool
	Protected             bool
	Navigation            bool
}

type PageEntry struct {
	Controller       string        `json:"controller"`
	Action           string        `json:"action"`
	Section          string        `json:"section,omitempty"`
	Menu             string        `json:"menu,omitempty"`
	Template         string        `json:"template,omitempty"`
	Permission       string        `json:"permission,omitempty"`
	Ownership        PageOwnership `json:"ownership,omitempty"`
	Protected        bool          `json:"protected"`
	Navigation       bool          `json:"navigation"`
	Params           []PageParam   `json:"params,omitempty"`
	RequiredFeatures []PageFeature `json:"required_features,omitempty"`
	PagePath         string        `json:"page_path"`
	RenderPath       string        `json:"render_path"`
	DirectPath       string        `json:"direct_path"`
}

func SessionPageSpecs() []PageSpec {
	return []PageSpec{
		{
			Controller:            "login",
			Action:                "index",
			IncludeControllerRoot: true,
			Section:               "auth",
			Menu:                  "login",
			Template:              "login/index.html",
			Navigation:            true,
		},
		{
			Controller: "login",
			Action:     "register",
			Section:    "auth",
			Menu:       "register",
			Template:   "login/register.html",
			Navigation: true,
			RequiredFeatures: []PageFeature{
				PageFeatureAllowUserRegister,
			},
		},
	}
}

func ProtectedPageSpecs() []PageSpec {
	return []PageSpec{
		{
			Controller:            "index",
			Action:                "index",
			IncludeControllerRoot: true,
			Protected:             true,
			Section:               "dashboard",
			Menu:                  "index",
			Template:              "index/index.html",
			Navigation:            true,
		},
		{
			Controller: "index",
			Action:     "help",
			Protected:  true,
			Section:    "dashboard",
			Menu:       "help",
			Template:   "index/help.html",
			Navigation: true,
		},
		{
			Controller: "index",
			Action:     "tcp",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "tcp",
			Template:   "index/list.html",
			Navigation: true,
			Params: []PageParam{
				{Name: "type", In: "query", Type: "string", Required: false},
			},
		},
		{
			Controller: "index",
			Action:     "udp",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "udp",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "socks5",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "socks5",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "http",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "http",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "mix",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "mix",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "file",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "file",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "secret",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "secret",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "p2p",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "p2p",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "all",
			Permission: webservice.PermissionTunnelsRead,
			Protected:  true,
			Section:    "clients",
			Menu:       "client",
			Template:   "index/list.html",
			Navigation: false,
			Params:     []PageParam{{Name: "client_id", In: "query", Type: "int", Required: false}},
		},
		{
			Controller: "index",
			Action:     "add",
			Permission: webservice.PermissionTunnelsCreate,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "add",
			Template:   "index/add.html",
			Navigation: false,
			Params: []PageParam{
				{Name: "type", In: "query", Type: "string", Required: false},
				{Name: "client_id", In: "query", Type: "int", Required: false},
			},
		},
		{
			Controller: "index",
			Action:     "edit",
			Permission: webservice.PermissionTunnelsRead,
			Ownership:  PageOwnershipTunnel,
			Protected:  true,
			Section:    "tunnels",
			Menu:       "edit",
			Template:   "index/edit.html",
			Navigation: false,
			Params:     []PageParam{{Name: "id", In: "query", Type: "int", Required: true}},
		},
		{
			Controller: "index",
			Action:     "host",
			Permission: webservice.PermissionHostsRead,
			Protected:  true,
			Section:    "hosts",
			Menu:       "host",
			Template:   "index/list.html",
			Navigation: true,
			Params:     []PageParam{{Name: "type", In: "query", Type: "string", Required: false}},
		},
		{
			Controller: "index",
			Action:     "hostlist",
			Permission: webservice.PermissionHostsRead,
			Protected:  true,
			Section:    "hosts",
			Menu:       "host",
			Template:   "index/hlist.html",
			Navigation: false,
			Params:     []PageParam{{Name: "client_id", In: "query", Type: "int", Required: false}},
		},
		{
			Controller: "index",
			Action:     "addhost",
			Permission: webservice.PermissionHostsCreate,
			Protected:  true,
			Section:    "hosts",
			Menu:       "host",
			Template:   "index/hadd.html",
			Navigation: false,
			Params:     []PageParam{{Name: "client_id", In: "query", Type: "int", Required: false}},
		},
		{
			Controller: "index",
			Action:     "edithost",
			Permission: webservice.PermissionHostsRead,
			Ownership:  PageOwnershipHost,
			Protected:  true,
			Section:    "hosts",
			Menu:       "host",
			Template:   "index/hedit.html",
			Navigation: false,
			Params:     []PageParam{{Name: "id", In: "query", Type: "int", Required: true}},
		},
		{
			Controller:            "client",
			Action:                "list",
			Permission:            webservice.PermissionClientsRead,
			IncludeControllerRoot: true,
			Protected:             true,
			Section:               "clients",
			Menu:                  "client",
			Template:              "client/list.html",
			Navigation:            true,
		},
		{
			Controller: "client",
			Action:     "add",
			Permission: webservice.PermissionClientsCreate,
			Protected:  true,
			Section:    "clients",
			Menu:       "client",
			Template:   "client/add.html",
			Navigation: true,
		},
		{
			Controller: "client",
			Action:     "edit",
			Permission: webservice.PermissionClientsRead,
			Ownership:  PageOwnershipClient,
			Protected:  true,
			Section:    "clients",
			Menu:       "client",
			Template:   "client/edit.html",
			Navigation: false,
			Params:     []PageParam{{Name: "id", In: "query", Type: "int", Required: true}},
		},
		{
			Controller:            "global",
			Action:                "index",
			Permission:            webservice.PermissionGlobalManage,
			IncludeControllerRoot: true,
			Protected:             true,
			Section:               "global",
			Menu:                  "global",
			Template:              "global/index.html",
			Navigation:            true,
		},
		{
			Controller: "global",
			Action:     "banlist",
			Permission: webservice.PermissionGlobalManage,
			Protected:  true,
			Section:    "global",
			Menu:       "banlist",
			Template:   "global/banlist.html",
			Navigation: true,
		},
	}
}

func AllPageSpecs() []PageSpec {
	specs := make([]PageSpec, 0, len(SessionPageSpecs())+len(ProtectedPageSpecs()))
	specs = append(specs, SessionPageSpecs()...)
	specs = append(specs, ProtectedPageSpecs()...)
	return specs
}

func AvailablePageSpecs(cfg *servercfg.Snapshot, specs []PageSpec) []PageSpec {
	filtered := make([]PageSpec, 0, len(specs))
	for _, spec := range specs {
		if PageSpecEnabled(cfg, spec) {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

func PageSpecEnabled(cfg *servercfg.Snapshot, spec PageSpec) bool {
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

func FindPageSpec(cfg *servercfg.Snapshot, controller, action string) (PageSpec, bool) {
	controller = strings.TrimSpace(controller)
	action = strings.TrimSpace(action)
	for _, spec := range AllPageSpecs() {
		if spec.Controller != controller || spec.Action != action {
			continue
		}
		if !PageSpecEnabled(cfg, spec) {
			return PageSpec{}, false
		}
		return spec, true
	}
	return PageSpec{}, false
}

func VisiblePageEntries(cfg *servercfg.Snapshot, baseURL string, actor *Actor, authz webservice.AuthorizationService) []PageEntry {
	entries := make([]PageEntry, 0)
	if authz == nil {
		authz = webservice.DefaultAuthorizationService{}
	}
	principal := authz.NormalizePrincipal(PrincipalFromActor(actor))
	for _, spec := range AvailablePageSpecs(cfg, SessionPageSpecs()) {
		if pageEntry, ok := visiblePageEntry(baseURL, spec, principal, authz); ok {
			entries = append(entries, pageEntry)
		}
	}
	for _, spec := range AvailablePageSpecs(cfg, ProtectedPageSpecs()) {
		if pageEntry, ok := visiblePageEntry(baseURL, spec, principal, authz); ok {
			entries = append(entries, pageEntry)
		}
	}
	return entries
}

func visiblePageEntry(baseURL string, spec PageSpec, principal webservice.Principal, authz webservice.AuthorizationService) (PageEntry, bool) {
	if spec.Protected && !principal.Authenticated {
		return PageEntry{}, false
	}
	if permission := strings.TrimSpace(spec.Permission); permission != "" && !authz.Allows(principal, permission) {
		return PageEntry{}, false
	}
	return pageEntryFromSpec(baseURL, spec), true
}

func pageEntryFromSpec(baseURL string, spec PageSpec) PageEntry {
	return PageEntry{
		Controller:       spec.Controller,
		Action:           spec.Action,
		Section:          spec.Section,
		Menu:             spec.Menu,
		Template:         spec.Template,
		Permission:       spec.Permission,
		Ownership:        spec.Ownership,
		Protected:        spec.Protected,
		Navigation:       spec.Navigation,
		Params:           copyPageParams(spec.Params),
		RequiredFeatures: copyPageFeatures(spec.RequiredFeatures),
		PagePath:         joinBase(baseURL, "/auth/page/"+spec.Controller+"/"+spec.Action),
		RenderPath:       joinBase(baseURL, "/auth/render/"+spec.Controller+"/"+spec.Action),
		DirectPath:       joinBase(baseURL, "/"+spec.Controller+"/"+spec.Action),
	}
}

func copyPageParams(params []PageParam) []PageParam {
	if len(params) == 0 {
		return nil
	}
	copied := make([]PageParam, len(params))
	copy(copied, params)
	return copied
}

func copyPageFeatures(features []PageFeature) []PageFeature {
	if len(features) == 0 {
		return nil
	}
	copied := make([]PageFeature, len(features))
	copy(copied, features)
	return copied
}

func pageFeatureEnabled(cfg *servercfg.Snapshot, feature PageFeature) bool {
	switch feature {
	case PageFeatureAllowUserRegister:
		return cfg.Feature.AllowUserRegister
	default:
		return true
	}
}
