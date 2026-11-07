package service

import (
	"errors"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/textutil"
	"github.com/djylb/nps/server/ports"
)

type Repository interface {
	RangeClients(func(*model.Client) bool)
	RangeTunnels(func(*model.Tunnel) bool)
	RangeHosts(func(*model.Host) bool)
	ListVisibleClients(ListClientsInput) ([]*model.Client, int)
	NextClientID() int
	NextTunnelID() int
	NextHostID() int
	CreateClient(*model.Client) error
	GetClient(int) (*model.Client, error)
	SaveClient(*model.Client) error
	PersistClients() error
	PersistGlobal() error
	DeleteClient(int) error
	VerifyUserName(string, int) bool
	VerifyVKey(string, int) bool
	ReplaceClientVKeyIndex(oldKey, newKey string, clientID int)
	CreateTunnel(*model.Tunnel) error
	GetTunnel(int) (*model.Tunnel, error)
	SaveTunnel(*model.Tunnel) error
	DeleteTunnelRecord(int) error
	CreateHost(*model.Host) error
	GetHost(int) (*model.Host, error)
	SaveHost(*model.Host, string) error
	FindReusableCert(host string, excludeID, clientID int) (*model.Host, error)
	PersistHosts() error
	DeleteHostRecord(int) error
	HostExists(*model.Host) bool
	GetGlobal() *model.Glob
	SaveGlobal(*model.Glob) error
	ClientOwnsTunnel(int, int) bool
	ClientOwnsHost(int, int) bool
}

type Runtime interface {
	DashboardData(bool) map[string]interface{}
	ListClients(offset, limit int, search, sort, order string, clientID int) ([]*model.Client, int)
	PingClient(int, string) int
	DisconnectClient(int)
	ClientConnected(int) bool
	RequestClientUpdate(int) (int, error)
	DeleteClientResources(int)
	GenerateTunnelPort(*model.Tunnel) int
	TunnelPortAvailable(*model.Tunnel) bool
	ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*model.Tunnel, int)
	AddTunnel(*model.Tunnel) error
	StopTunnel(int) error
	StartTunnel(int) error
	DeleteTunnel(int) error
	ListHosts(offset, limit, clientID int, search, sort, order string) ([]*model.Host, int)
	RemoveHostCache(int)
	TunnelRunning(int) bool
	// CanUpdateSelf reports whether the server may be updated from the web.
	CanUpdateSelf() bool
	// UpdateSelf updates the server to the latest release and restarts it.
	UpdateSelf() (string, error)
}

type Backend struct {
	Repository Repository
	Runtime    Runtime
}

// DefaultBackend is the backend of the store without a running server.
func DefaultBackend() Backend {
	return NewBackend(nil)
}

// NewBackend is the backend of the store and of runtime, the running
// server; a nil runtime means none runs.
func NewBackend(runtime Runtime) Backend {
	if runtime == nil {
		runtime = offlineRuntime{}
	} else {
		runtime = bridgeErrors{runtime}
	}
	return Backend{
		Repository: defaultRepository{},
		Runtime:    runtime,
	}
}

func (b Backend) repo() Repository {
	if b.Repository != nil {
		return b.Repository
	}
	return DefaultBackend().Repository
}

func (b Backend) runtime() Runtime {
	if b.Runtime != nil {
		return b.Runtime
	}
	return DefaultBackend().Runtime
}

type defaultRepository struct{}

func (defaultRepository) RangeClients(fn func(*model.Client) bool) {
	file.GetDb().JsonDb.Clients.Range(func(_, value interface{}) bool {
		return fn(value.(*model.Client))
	})
}

func (defaultRepository) RangeTunnels(fn func(*model.Tunnel) bool) {
	file.GetDb().RangeTunnels(fn)
}

func (defaultRepository) RangeHosts(fn func(*model.Host) bool) {
	file.GetDb().RangeHosts(fn)
}

func (defaultRepository) ListVisibleClients(input ListClientsInput) ([]*model.Client, int) {
	visible := visibleClientSet(input.Visibility)
	if input.Visibility.IsAdmin || len(visible) == 1 {
		return file.GetDb().GetClientList(input.Offset, input.Limit, input.Search, input.Sort, input.Order, visibleClientID(input.Visibility))
	}
	if len(visible) == 0 {
		return nil, 0
	}

	list := make([]*model.Client, 0)
	var count int
	originLimit := input.Limit
	searchID := textutil.GetIntNoErrByStr(input.Search)
	keys := file.GetMapKeys(&file.GetDb().JsonDb.Clients, true, input.Sort, input.Order)
	for _, key := range keys {
		value, ok := file.GetDb().JsonDb.Clients.Load(key)
		if !ok {
			continue
		}
		client := value.(*model.Client)
		if client.NoDisplay {
			continue
		}
		if _, ok := visible[client.Id]; !ok {
			continue
		}
		if input.Search != "" &&
			client.Id != searchID &&
			!textutil.ContainsFold(client.VerifyKey, input.Search) &&
			!textutil.ContainsFold(client.Remark, input.Search) {
			continue
		}
		count++
		if input.Offset--; input.Offset < 0 {
			if originLimit == 0 {
				list = append(list, client)
			} else if input.Limit--; input.Limit >= 0 {
				list = append(list, client)
			}
		}
	}
	return list, count
}

func (defaultRepository) NextClientID() int {
	return int(file.GetDb().JsonDb.GetClientId())
}

func (defaultRepository) NextTunnelID() int {
	return file.GetDb().NextTunnelID()
}

func (defaultRepository) NextHostID() int {
	return file.GetDb().NextHostID()
}

func (defaultRepository) CreateClient(client *model.Client) error {
	return file.GetDb().NewClient(client)
}

func (defaultRepository) GetClient(id int) (*model.Client, error) {
	return file.GetDb().GetClient(id)
}

func (defaultRepository) SaveClient(_ *model.Client) error {
	return file.GetDb().JsonDb.StoreClientsToJsonFile()
}

func (defaultRepository) PersistClients() error {
	return file.GetDb().JsonDb.StoreClientsToJsonFile()
}

func (defaultRepository) PersistGlobal() error {
	return file.GetDb().JsonDb.StoreGlobalToJsonFile()
}

func (defaultRepository) DeleteClient(id int) error {
	return file.GetDb().DelClient(id)
}

func (defaultRepository) VerifyUserName(username string, exceptID int) bool {
	return file.GetDb().VerifyUserName(username, exceptID)
}

func (defaultRepository) VerifyVKey(vkey string, exceptID int) bool {
	return file.GetDb().VerifyVkey(vkey, exceptID)
}

func (defaultRepository) ReplaceClientVKeyIndex(oldKey, newKey string, clientID int) {
	file.GetDb().ReplaceClientVKey(oldKey, newKey, clientID)
}

func (defaultRepository) CreateTunnel(tunnel *model.Tunnel) error {
	return file.GetDb().NewTask(tunnel)
}

func (defaultRepository) GetTunnel(id int) (*model.Tunnel, error) {
	return file.GetDb().GetTask(id)
}

func (defaultRepository) SaveTunnel(tunnel *model.Tunnel) error {
	return file.GetDb().UpdateTask(tunnel)
}

func (defaultRepository) DeleteTunnelRecord(id int) error {
	return file.GetDb().DelTask(id)
}

func (defaultRepository) CreateHost(host *model.Host) error {
	return file.GetDb().NewHost(host)
}

func (defaultRepository) GetHost(id int) (*model.Host, error) {
	return file.GetDb().GetHostById(id)
}

func (defaultRepository) SaveHost(host *model.Host, oldHost string) error {
	return file.GetDb().SaveHost(host, oldHost)
}

func (defaultRepository) FindReusableCert(host string, excludeID, clientID int) (*model.Host, error) {
	return file.GetDb().FindReusableCert(host, excludeID, clientID)
}

func (defaultRepository) PersistHosts() error {
	return file.GetDb().JsonDb.StoreHostToJsonFile()
}

func (defaultRepository) DeleteHostRecord(id int) error {
	return file.GetDb().DelHost(id)
}

func (defaultRepository) HostExists(host *model.Host) bool {
	return file.GetDb().IsHostExist(host)
}

func (defaultRepository) GetGlobal() *model.Glob {
	return file.GetDb().GetGlobal()
}

func (defaultRepository) SaveGlobal(glob *model.Glob) error {
	return file.GetDb().SaveGlobal(glob)
}

func (defaultRepository) ClientOwnsTunnel(clientID, tunnelID int) bool {
	if clientID <= 0 || tunnelID <= 0 {
		return false
	}
	if v, ok := file.GetDb().JsonDb.Tasks.Load(tunnelID); ok {
		return v.(*model.Tunnel).Client.Id == clientID
	}
	return false
}

func (defaultRepository) ClientOwnsHost(clientID, hostID int) bool {
	if clientID <= 0 || hostID <= 0 {
		return false
	}
	if v, ok := file.GetDb().JsonDb.Hosts.Load(hostID); ok {
		return v.(*model.Host).Client.Id == clientID
	}
	return false
}

// offlineRuntime is the runtime while no server runs: no client is
// connected, no task runs and none can be started.
type offlineRuntime struct{}

func (offlineRuntime) DashboardData(bool) map[string]interface{} { return map[string]interface{}{} }

func (offlineRuntime) ListClients(offset, limit int, search, sort, order string, clientID int) ([]*model.Client, int) {
	return file.GetDb().GetClientList(offset, limit, search, sort, order, clientID)
}

func (offlineRuntime) PingClient(int, string) int { return -1 }

func (offlineRuntime) DisconnectClient(int) {}

func (offlineRuntime) ClientConnected(int) bool { return false }

func (offlineRuntime) RequestClientUpdate(int) (int, error) { return 0, ErrClientOffline }

func (offlineRuntime) DeleteClientResources(int) {}

func (offlineRuntime) GenerateTunnelPort(tunnel *model.Tunnel) int {
	return ports.GenerateTunnelPort(tunnel)
}

func (offlineRuntime) TunnelPortAvailable(tunnel *model.Tunnel) bool {
	return ports.TestTunnelPort(tunnel)
}

func (offlineRuntime) ListTunnels(int, int, string, int, string, string, string) ([]*model.Tunnel, int) {
	return nil, 0
}

func (offlineRuntime) AddTunnel(*model.Tunnel) error { return ErrServerNotRunning }

func (offlineRuntime) StopTunnel(int) error { return ErrServerNotRunning }

func (offlineRuntime) StartTunnel(int) error { return ErrServerNotRunning }

func (offlineRuntime) DeleteTunnel(int) error { return ErrServerNotRunning }

func (offlineRuntime) ListHosts(offset, limit, clientID int, search, _, _ string) ([]*model.Host, int) {
	return file.GetDb().GetHost(offset, limit, clientID, search)
}

func (offlineRuntime) RemoveHostCache(int) {}

func (offlineRuntime) TunnelRunning(int) bool { return false }

func (offlineRuntime) CanUpdateSelf() bool { return false }

func (offlineRuntime) UpdateSelf() (string, error) { return "", ErrServerUpdateUnavailable }

// bridgeErrors turns the errors of the bridge that the web manager tells
// apart into its own.
type bridgeErrors struct{ Runtime }

func (r bridgeErrors) RequestClientUpdate(id int) (int, error) {
	n, err := r.Runtime.RequestClientUpdate(id)
	switch {
	case errors.Is(err, bridge.ErrClientOffline):
		err = ErrClientOffline
	case errors.Is(err, bridge.ErrUpdateUnsupported):
		err = ErrClientUpdateUnsupported
	}
	return n, err
}
