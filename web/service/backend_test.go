package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type stubRepository struct {
	rangeClients      func(func(*file.Client) bool)
	listVisibleClient func(ListClientsInput) ([]*file.Client, int)
	nextClientID      func() int
	createClient      func(*file.Client) error
	nextTunnelID      func() int
	createTunnel      func(*file.Tunnel) error
	getClient         func(int) (*file.Client, error)
}

func (s stubRepository) RangeClients(fn func(*file.Client) bool) {
	if s.rangeClients != nil {
		s.rangeClients(fn)
	}
}

func (stubRepository) RangeTunnels(func(*file.Tunnel) bool) {}
func (stubRepository) RangeHosts(func(*file.Host) bool)     {}
func (s stubRepository) ListVisibleClients(input ListClientsInput) ([]*file.Client, int) {
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
func (s stubRepository) CreateClient(client *file.Client) error {
	if s.createClient != nil {
		return s.createClient(client)
	}
	return nil
}
func (s stubRepository) GetClient(id int) (*file.Client, error) {
	if s.getClient != nil {
		return s.getClient(id)
	}
	return nil, nil
}
func (stubRepository) SaveClient(*file.Client) error              { return nil }
func (stubRepository) PersistClients() error                      { return nil }
func (stubRepository) PersistGlobal() error                       { return nil }
func (stubRepository) DeleteClient(int) error                     { return nil }
func (stubRepository) VerifyUserName(string, int) bool            { return true }
func (stubRepository) VerifyVKey(string, int) bool                { return true }
func (stubRepository) ReplaceClientVKeyIndex(string, string, int) {}
func (s stubRepository) CreateTunnel(tunnel *file.Tunnel) error {
	if s.createTunnel != nil {
		return s.createTunnel(tunnel)
	}
	return nil
}
func (stubRepository) GetTunnel(int) (*file.Tunnel, error) { return nil, nil }
func (stubRepository) SaveTunnel(*file.Tunnel) error       { return nil }
func (stubRepository) DeleteTunnelRecord(int) error        { return nil }
func (stubRepository) CreateHost(*file.Host) error         { return nil }
func (stubRepository) GetHost(int) (*file.Host, error)     { return nil, nil }
func (stubRepository) SaveHost(*file.Host, string) error   { return nil }
func (stubRepository) PersistHosts() error                 { return nil }
func (stubRepository) DeleteHostRecord(int) error          { return nil }
func (stubRepository) HostExists(*file.Host) bool          { return false }
func (stubRepository) GetGlobal() *file.Glob               { return nil }
func (stubRepository) SaveGlobal(*file.Glob) error         { return nil }
func (stubRepository) ClientOwnsTunnel(int, int) bool      { return false }
func (stubRepository) ClientOwnsHost(int, int) bool        { return false }

type stubRuntime struct {
	dashboardData func(bool) map[string]interface{}
	generatePort  func(*file.Tunnel) int
	portAvailable func(*file.Tunnel) bool
}

func (s stubRuntime) DashboardData(force bool) map[string]interface{} {
	if s.dashboardData != nil {
		return s.dashboardData(force)
	}
	return nil
}

func (stubRuntime) ListClients(int, int, string, string, string, int) ([]*file.Client, int) {
	return nil, 0
}
func (stubRuntime) PingClient(int, string) int           { return 0 }
func (stubRuntime) DisconnectClient(int)                 {}
func (stubRuntime) RequestClientUpdate(int) (int, error) { return 0, nil }
func (stubRuntime) DeleteClientResources(int)            {}
func (s stubRuntime) GenerateTunnelPort(tunnel *file.Tunnel) int {
	if s.generatePort != nil {
		return s.generatePort(tunnel)
	}
	return 0
}
func (s stubRuntime) TunnelPortAvailable(tunnel *file.Tunnel) bool {
	if s.portAvailable != nil {
		return s.portAvailable(tunnel)
	}
	return true
}
func (stubRuntime) ListTunnels(int, int, string, int, string, string, string) ([]*file.Tunnel, int) {
	return nil, 0
}
func (stubRuntime) AddTunnel(*file.Tunnel) error { return nil }
func (stubRuntime) StopTunnel(int) error         { return nil }
func (stubRuntime) StartTunnel(int) error        { return nil }
func (stubRuntime) DeleteTunnel(int) error       { return nil }
func (stubRuntime) ListHosts(int, int, int, string, string, string) ([]*file.Host, int) {
	return nil, 0
}
func (stubRuntime) RemoveHostCache(int) {}

func TestDefaultClientServiceUsesInjectedRepositoryForList(t *testing.T) {
	repo := stubRepository{
		listVisibleClient: func(ListClientsInput) ([]*file.Client, int) {
			return []*file.Client{{Id: 7, Remark: "from-backend"}}, 1
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
				getClient: func(id int) (*file.Client, error) {
					return &file.Client{Id: id, Status: true, Flow: &file.Flow{}}, nil
				},
				createTunnel: func(tunnel *file.Tunnel) error {
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
				generatePort: func(tunnel *file.Tunnel) int {
					if tunnel == nil || tunnel.Mode != "tcp" {
						t.Fatalf("GenerateTunnelPort() tunnel = %#v, want tcp tunnel", tunnel)
					}
					return 18080
				},
				portAvailable: func(tunnel *file.Tunnel) bool {
					return tunnel != nil && tunnel.Port == 18080 && tunnel.Mode == "tcp"
				},
			},
		},
	}

	result, err := service.AddTunnel(AddTunnelInput{
		ClientID: 1,
		Mode:     "tcp",
	})
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
				rangeClients: func(fn func(*file.Client) bool) {
					fn(&file.Client{
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
				createClient: func(client *file.Client) error {
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
