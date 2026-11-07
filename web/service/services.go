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
	Config      ConfigTransferService
	// Runtime is the running server, or one that runs nothing.
	Runtime Runtime
}

// New returns the services of the store and of runtime, the running server;
// a nil runtime means none runs.
func New(runtime Runtime) Services {
	backend := NewBackend(runtime)
	system := DefaultSystemService{Runtime: backend.Runtime}
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
		Config:      DefaultConfigTransferService{Backend: backend},
		Runtime:     backend.Runtime,
		Pages: DefaultPageService{
			Clients: clients,
			Globals: globals,
			Index:   index,
			System:  system,
		},
	}
}
