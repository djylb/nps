package service

import (
	"testing"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/servercfg"
)

type stubRepository struct {
	rangeClients      func(func(*model.Client) bool)
	listVisibleClient func(ListClientsInput) ([]*model.Client, int)
	nextClientID      func() int
	createClient      func(*model.Client) error
	nextTunnelID      func() int
	createTunnel      func(*model.Tunnel) error
	getClient         func(int) (*model.Client, error)
}

func (s stubRepository) RangeClients(fn func(*model.Client) bool) {
	if s.rangeClients != nil {
		s.rangeClients(fn)
	}
}

func (stubRepository) RangeTunnels(func(*model.Tunnel) bool) {}
func (stubRepository) RangeHosts(func(*model.Host) bool)     {}
func (s stubRepository) ListVisibleClients(input ListClientsInput) ([]*model.Client, int) {
	if s.listVisibleClient != nil {
		return s.listVisibleClient(input)
	}
	return nil, 0
}
func (s stubRepository) NextClientID() int {
	if s.nextClientID != nil {
		return s.nextClientID()
	}
	return 0
}
func (s stubRepository) NextTunnelID() int {
	if s.nextTunnelID != nil {
		return s.nextTunnelID()
	}
	return 0
}
func (stubRepository) NextHostID() int { return 0 }
func (s stubRepository) CreateClient(client *model.Client) error {
	if s.createClient != nil {
		return s.createClient(client)
	}
	return nil
}
func (s stubRepository) GetClient(id int) (*model.Client, error) {
	if s.getClient != nil {
		return s.getClient(id)
	}
	return nil, nil
}
func (stubRepository) SaveClient(*model.Client) error             { return nil }
func (stubRepository) PersistClients() error                      { return nil }
func (stubRepository) PersistGlobal() error                       { return nil }
func (stubRepository) DeleteClient(int) error                     { return nil }
func (stubRepository) VerifyUserName(string, int) bool            { return true }
func (stubRepository) VerifyVKey(string, int) bool                { return true }
func (stubRepository) ReplaceClientVKeyIndex(string, string, int) {}
func (s stubRepository) CreateTunnel(tunnel *model.Tunnel) error {
	if s.createTunnel != nil {
		return s.createTunnel(tunnel)
	}
	return nil
}
func (stubRepository) GetTunnel(int) (*model.Tunnel, error) { return nil, nil }
func (stubRepository) SaveTunnel(*model.Tunnel) error       { return nil }
func (stubRepository) DeleteTunnelRecord(int) error         { return nil }
func (stubRepository) CreateHost(*model.Host) error         { return nil }
func (stubRepository) GetHost(int) (*model.Host, error)     { return nil, nil }
func (stubRepository) SaveHost(*model.Host, string) error   { return nil }
func (stubRepository) PersistHosts() error                  { return nil }
func (stubRepository) FindReusableCert(string, int, int) (*model.Host, error) {
	return nil, nil
}
func (stubRepository) DeleteHostRecord(int) error         { return nil }
func (stubRepository) HostExists(*model.Host) bool        { return false }
func (stubRepository) GetGlobal() *model.Glob             { return nil }
func (stubRepository) SaveGlobal(*model.Glob) error       { return nil }
func (stubRepository) ClientEntryCount(*model.Client) int { return 0 }
func (stubRepository) GlobalDenied(string, string) bool   { return false }
func (stubRepository) ClientOwnsTunnel(int, int) bool     { return false }
func (stubRepository) ClientOwnsHost(int, int) bool       { return false }

type stubRuntime struct {
	dashboardData func(bool) map[string]interface{}
	generatePort  func(*model.Tunnel) int
	portAvailable func(*model.Tunnel) bool
}

func (s stubRuntime) DashboardData(force bool) map[string]interface{} {
	if s.dashboardData != nil {
		return s.dashboardData(force)
	}
	return nil
}

func (stubRuntime) ListClients(int, int, string, string, string, int) ([]*model.Client, int) {
	return nil, 0
}
func (stubRuntime) PingClient(int, string) int           { return 0 }
func (stubRuntime) DisconnectClient(int)                 {}
func (stubRuntime) ClientConnected(int) bool             { return false }
func (stubRuntime) RequestClientUpdate(int) (int, error) { return 0, nil }
func (stubRuntime) DeleteClientResources(int)            {}
func (s stubRuntime) GenerateTunnelPort(tunnel *model.Tunnel) int {
	if s.generatePort != nil {
		return s.generatePort(tunnel)
	}
	return 0
}
func (s stubRuntime) TunnelPortAvailable(tunnel *model.Tunnel) bool {
	if s.portAvailable != nil {
		return s.portAvailable(tunnel)
	}
	return true
}
func (stubRuntime) ListTunnels(int, int, string, int, string, string, string) ([]*model.Tunnel, int) {
	return nil, 0
}
func (stubRuntime) AddTunnel(*model.Tunnel) error { return nil }
func (stubRuntime) StopTunnel(int) error          { return nil }
func (stubRuntime) StartTunnel(int) error         { return nil }
func (stubRuntime) DeleteTunnel(int) error        { return nil }
func (stubRuntime) ListHosts(int, int, int, string, string, string) ([]*model.Host, int) {
	return nil, 0
}
func (stubRuntime) RemoveHostCache(int)    {}
func (stubRuntime) TunnelRunning(int) bool { return false }

func (stubRuntime) CanUpdateSelf() bool { return false }

func (stubRuntime) UpdateSelf() (string, error) { return "", nil }

func (stubRuntime) ReloadConfig() servercfg.Report { return servercfg.Report{} }

func TestDefaultClientServiceUsesInjectedRepositoryForList(t *testing.T) {
	repo := stubRepository{
		listVisibleClient: func(ListClientsInput) ([]*model.Client, int) {
			return []*model.Client{{Id: 7, Remark: "from-backend"}}, 1
		},
	}
	service := DefaultClientService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{}
		},
		Backend: Backend{Repository: repo, Runtime: stubRuntime{}},
	}

	result := service.List(ListClientsInput{})
	if result.Total != 1 || len(result.Rows) != 1 || result.Rows[0].Remark != "from-backend" {
		t.Fatalf("List() = %+v, want injected repository result", result)
	}
}

func TestDefaultIndexServiceUsesInjectedRuntimeForDashboard(t *testing.T) {
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{},
			Runtime: stubRuntime{
				dashboardData: func(force bool) map[string]interface{} {
					return map[string]interface{}{"force": force, "source": "backend"}
				},
			},
		},
	}

	data := service.DashboardData(true)
	if data["source"] != "backend" || data["force"] != true {
		t.Fatalf("DashboardData() = %+v, want injected runtime data", data)
	}
}

func TestDefaultIndexServiceAddTunnelUsesInjectedRuntimePortPolicy(t *testing.T) {
	var created struct {
		ID       int
		Port     int
		ClientID int
		Mode     string
	}
	var createdSet bool
	service := DefaultIndexService{
		Backend: Backend{
			Repository: stubRepository{
				nextTunnelID: func() int { return 9 },
				getClient: func(id int) (*model.Client, error) {
					return &model.Client{Id: id, Status: true, Flow: &model.Flow{}}, nil
				},
				createTunnel: func(tunnel *model.Tunnel) error {
					created = struct {
						ID       int
						Port     int
						ClientID int
						Mode     string
					}{
						ID:       tunnel.Id,
						Port:     tunnel.Port,
						ClientID: tunnel.Client.Id,
						Mode:     tunnel.Mode,
					}
					createdSet = true
					return nil
				},
			},
			Runtime: stubRuntime{
				generatePort: func(tunnel *model.Tunnel) int {
					if tunnel == nil || tunnel.Mode != "tcp" {
						t.Fatalf("GenerateTunnelPort() tunnel = %#v, want tcp tunnel", tunnel)
					}
					return 18080
				},
				portAvailable: func(tunnel *model.Tunnel) bool {
					return tunnel != nil && tunnel.Port == 18080 && tunnel.Mode == "tcp"
				},
			},
		},
	}

	result, err := service.AddTunnel(TunnelInput{ClientID: 1, Tunnel: &model.Tunnel{Mode: "tcp"}})
	if err != nil {
		t.Fatalf("AddTunnel() error = %v", err)
	}
	if !createdSet || created.Port != 18080 || created.ID != 9 || created.ClientID != 1 || created.Mode != "tcp" {
		t.Fatalf("AddTunnel() created tunnel = %+v, want runtime-selected port 18080 and id 9", created)
	}
	if result.ID != 9 || result.ClientID != 1 || result.Mode != "tcp" {
		t.Fatalf("AddTunnel() = %+v, want runtime-backed mutation", result)
	}
}

func TestDefaultAuthServiceUsesInjectedRepository(t *testing.T) {
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Feature: servercfg.FeatureConfig{AllowUserLogin: true},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*model.Client) bool) {
					fn(&model.Client{
						Id:          42,
						Status:      true,
						WebUserName: "demo",
						WebPassword: "secret",
					})
				},
			},
		},
	}

	identity, err := service.Authenticate(AuthenticateInput{
		Username: "demo",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity == nil || len(identity.ClientIDs) != 1 || identity.ClientIDs[0] != 42 {
		t.Fatalf("Authenticate() = %+v, want injected repository-backed identity", identity)
	}
}

func TestDefaultAuthServiceRegisterUserUsesInjectedRepository(t *testing.T) {
	var created struct {
		ID       int
		Username string
		Password string
	}
	var createdSet bool
	service := DefaultAuthService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Web: servercfg.WebConfig{Username: "admin"},
			}
		},
		Backend: Backend{
			Repository: stubRepository{
				nextClientID: func() int { return 73 },
				createClient: func(client *model.Client) error {
					created = struct {
						ID       int
						Username string
						Password string
					}{
						ID:       client.Id,
						Username: client.WebUserName,
						Password: client.WebPassword,
					}
					createdSet = true
					return nil
				},
			},
		},
	}

	result, err := service.RegisterUser(RegisterUserInput{
		Username: "demo",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("RegisterUser() error = %v", err)
	}
	if !createdSet || created.ID != 73 || created.Username != "demo" || created.Password != "secret" {
		t.Fatalf("RegisterUser() created client = %+v, want injected repository-backed client", created)
	}
	if result == nil || len(result.ClientIDs) != 1 || result.ClientIDs[0] != 73 {
		t.Fatalf("RegisterUser() = %+v, want client id 73", result)
	}
}

type cacheOrderRuntime struct {
	stubRuntime
	removed func()
}

func (r cacheOrderRuntime) RemoveHostCache(int) { r.removed() }

// The transport cache entry goes after the edit: a request in between would
// cache a transport of the old settings again.
func TestEditHostRemovesCacheAfterEdit(t *testing.T) {
	host := &model.Host{Id: 1, Host: "a.example", Client: &model.Client{Id: 1}, Flow: &model.Flow{}, Target: &model.Target{}}
	r := &rateLimitRepository{stubRepository: stubRepository{getClient: func(int) (*model.Client, error) { return host.Client, nil }}, host: host}
	var hostAtRemoval string
	s := DefaultIndexService{Backend: Backend{Repository: r, Runtime: cacheOrderRuntime{removed: func() { hostAtRemoval = host.Host }}}}
	if _, err := s.EditHost(HostInput{ID: 1, Host: &model.Host{Host: "b.example"}}); err != nil {
		t.Fatal(err)
	}
	if hostAtRemoval != "b.example" {
		t.Fatalf("cache removed while the host was %q", hostAtRemoval)
	}
}
