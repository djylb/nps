package service

import "github.com/djylb/nps/lib/servercfg"

// Services groups the default application services behind replaceable interfaces.
// Swapping framework or storage backends should not require transport-layer rewrites.
type Services struct {
	System      SystemService
	Permissions PermissionResolver
	Auth        AuthService
	Authz       AuthorizationService
	LoginPolicy LoginPolicyService
	Clients     ClientService
	Globals     GlobalService
	Index       IndexService
	Pages       PageService
}

func New() Services {
	backend := DefaultBackend()
	system := DefaultSystemService{}
	resolver := DefaultPermissionResolver()
	loginPolicy := SharedLoginPolicy()
	clients := DefaultClientService{ConfigProvider: servercfg.Current, Backend: backend}
	globals := DefaultGlobalService{LoginPolicy: loginPolicy, Backend: backend}
	index := DefaultIndexService{Backend: backend}
	return Services{
		System:      system,
		Permissions: resolver,
		Auth:        DefaultAuthService{Resolver: resolver, ConfigProvider: servercfg.Current, Backend: backend},
		Authz:       DefaultAuthorizationService{Resolver: resolver, Backend: backend},
		LoginPolicy: loginPolicy,
		Clients:     clients,
		Globals:     globals,
		Index:       index,
		Pages: DefaultPageService{
			Clients: clients,
			Globals: globals,
			Index:   index,
			System:  system,
		},
	}
}
