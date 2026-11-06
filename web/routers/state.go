package routers

import (
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

// State centralizes router-side dependencies so transport code does not reach
// into global singletons directly.
type State struct {
	App            *webapi.App
	ConfigProvider func() *servercfg.Snapshot
}

func NewState(cfg *servercfg.Snapshot) *State {
	return NewStateWithApp(webapi.New(cfg))
}

func NewStateWithApp(app *webapi.App) *State {
	if app == nil {
		app = webapi.New(nil)
	}
	return &State{
		App:            app,
		ConfigProvider: app.CurrentConfig,
	}
}

func (s *State) CurrentConfig() *servercfg.Snapshot {
	if s != nil && s.ConfigProvider != nil {
		if cfg := s.ConfigProvider(); cfg != nil {
			return cfg
		}
	}
	if s != nil && s.App != nil {
		if cfg := s.App.CurrentConfig(); cfg != nil {
			return cfg
		}
	}
	return servercfg.Current()
}

func (s *State) BaseURL() string {
	return s.CurrentConfig().Web.BaseURL
}

func (s *State) LoginURL() string {
	return joinBase(s.BaseURL(), "/login/index")
}

func (s *State) AdminUsername() string {
	return s.CurrentConfig().Web.Username
}

func (s *State) PermissionResolver() webservice.PermissionResolver {
	if s != nil && s.App != nil && s.App.Services.Permissions != nil {
		return s.App.Services.Permissions
	}
	return webservice.DefaultPermissionResolver()
}

func (s *State) Authorization() webservice.AuthorizationService {
	if s != nil && s.App != nil && s.App.Services.Authz != nil {
		return s.App.Services.Authz
	}
	return webservice.DefaultAuthorizationService{Resolver: s.PermissionResolver()}
}

func (s *State) System() webservice.SystemService {
	if s != nil && s.App != nil && s.App.Services.System != nil {
		return s.App.Services.System
	}
	return webservice.DefaultSystemService{}
}

func (s *State) LoginPolicy() webservice.LoginPolicyService {
	if s != nil && s.App != nil && s.App.Services.LoginPolicy != nil {
		return s.App.Services.LoginPolicy
	}
	return webservice.SharedLoginPolicy()
}

func (s *State) AvailablePageSpecs(specs []webapi.PageSpec) []webapi.PageSpec {
	return webapi.AvailablePageSpecs(s.CurrentConfig(), specs)
}
