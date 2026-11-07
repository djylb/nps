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
	// ClientEntryCount is the number of tunnels and hosts of client.
	ClientEntryCount(*model.Client) int
	// GlobalDenied reports whether the global access lists deny addr, and
	// logs a denial; what names the listener in the log.
	GlobalDenied(addr, what string) bool
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

// NewBackend is the backend of the store db and of runtime, the running
// server; a nil runtime means none runs.
func NewBackend(db *file.DbUtils, runtime Runtime) Backend {
	if runtime == nil {
		runtime = offlineRuntime{db: db}
	} else {
		runtime = bridgeErrors{runtime}
	}
	return Backend{
		Repository: storeRepository{db: db},
		Runtime:    runtime,
	}
}

// storeRepository is the Repository of a store.
type storeRepository struct{ db *file.DbUtils }

func (r storeRepository) RangeClients(fn func(*model.Client) bool) {
	r.db.JsonDb.Clients.Range(func(_, value interface{}) bool {
		return fn(value.(*model.Client))
	})
}

func (r storeRepository) RangeTunnels(fn func(*model.Tunnel) bool) {
	r.db.RangeTunnels(fn)
}

func (r storeRepository) RangeHosts(fn func(*model.Host) bool) {
	r.db.RangeHosts(fn)
}

func (r storeRepository) ListVisibleClients(input ListClientsInput) ([]*model.Client, int) {
	visible := visibleClientSet(input.Visibility)
	if input.Visibility.IsAdmin || len(visible) == 1 {
		return r.db.GetClientList(input.Offset, input.Limit, input.Search, input.Sort, input.Order, visibleClientID(input.Visibility))
	}
	if len(visible) == 0 {
		return nil, 0
	}

	list := make([]*model.Client, 0)
	var count int
	originLimit := input.Limit
	searchID := textutil.GetIntNoErrByStr(input.Search)
	keys := file.GetMapKeys(&r.db.JsonDb.Clients, true, input.Sort, input.Order)
	for _, key := range keys {
		value, ok := r.db.JsonDb.Clients.Load(key)
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

func (r storeRepository) NextClientID() int {
	return int(r.db.JsonDb.GetClientId())
}

func (r storeRepository) NextTunnelID() int {
	return r.db.NextTunnelID()
}

func (r storeRepository) NextHostID() int {
	return r.db.NextHostID()
}

func (r storeRepository) CreateClient(client *model.Client) error {
	return r.db.NewClient(client)
}

func (r storeRepository) GetClient(id int) (*model.Client, error) {
	return r.db.GetClient(id)
}

func (r storeRepository) SaveClient(_ *model.Client) error {
	return r.db.JsonDb.StoreClientsToJsonFile()
}

func (r storeRepository) PersistClients() error {
	return r.db.JsonDb.StoreClientsToJsonFile()
}

func (r storeRepository) PersistGlobal() error {
	return r.db.JsonDb.StoreGlobalToJsonFile()
}

func (r storeRepository) DeleteClient(id int) error {
	return r.db.DelClient(id)
}

func (r storeRepository) VerifyUserName(username string, exceptID int) bool {
	return r.db.VerifyUserName(username, exceptID)
}

func (r storeRepository) VerifyVKey(vkey string, exceptID int) bool {
	return r.db.VerifyVkey(vkey, exceptID)
}

func (r storeRepository) ReplaceClientVKeyIndex(oldKey, newKey string, clientID int) {
	r.db.ReplaceClientVKey(oldKey, newKey, clientID)
}

func (r storeRepository) CreateTunnel(tunnel *model.Tunnel) error {
	return r.db.NewTask(tunnel)
}

func (r storeRepository) GetTunnel(id int) (*model.Tunnel, error) {
	return r.db.GetTask(id)
}

func (r storeRepository) SaveTunnel(tunnel *model.Tunnel) error {
	return r.db.UpdateTask(tunnel)
}

func (r storeRepository) DeleteTunnelRecord(id int) error {
	return r.db.DelTask(id)
}

func (r storeRepository) CreateHost(host *model.Host) error {
	return r.db.NewHost(host)
}

func (r storeRepository) GetHost(id int) (*model.Host, error) {
	return r.db.GetHostById(id)
}

func (r storeRepository) SaveHost(host *model.Host, oldHost string) error {
	return r.db.SaveHost(host, oldHost)
}

func (r storeRepository) FindReusableCert(host string, excludeID, clientID int) (*model.Host, error) {
	return r.db.FindReusableCert(host, excludeID, clientID)
}

func (r storeRepository) PersistHosts() error {
	return r.db.JsonDb.StoreHostToJsonFile()
}

func (r storeRepository) DeleteHostRecord(id int) error {
	return r.db.DelHost(id)
}

func (r storeRepository) HostExists(host *model.Host) bool {
	return r.db.IsHostExist(host)
}

func (r storeRepository) GetGlobal() *model.Glob {
	return r.db.GetGlobal()
}

func (r storeRepository) SaveGlobal(glob *model.Glob) error {
	return r.db.SaveGlobal(glob)
}

func (r storeRepository) ClientEntryCount(client *model.Client) int {
	return r.db.ClientEntryCount(client)
}

func (r storeRepository) GlobalDenied(addr, what string) bool {
	return r.db.GlobalDeniedString(addr, what)
}

func (r storeRepository) ClientOwnsTunnel(clientID, tunnelID int) bool {
	if clientID <= 0 || tunnelID <= 0 {
		return false
	}
	if v, ok := r.db.JsonDb.Tasks.Load(tunnelID); ok {
		return v.(*model.Tunnel).Client.Id == clientID
	}
	return false
}

func (r storeRepository) ClientOwnsHost(clientID, hostID int) bool {
	if clientID <= 0 || hostID <= 0 {
		return false
	}
	if v, ok := r.db.JsonDb.Hosts.Load(hostID); ok {
		return v.(*model.Host).Client.Id == clientID
	}
	return false
}

// offlineRuntime is the runtime while no server runs: no client is
// connected, no task runs and none can be started.
// Without a server there is no allow list: every port is allowed.
type offlineRuntime struct{ db *file.DbUtils }

func (offlineRuntime) DashboardData(bool) map[string]interface{} { return map[string]interface{}{} }

func (r offlineRuntime) ListClients(offset, limit int, search, sort, order string, clientID int) ([]*model.Client, int) {
	return r.db.GetClientList(offset, limit, search, sort, order, clientID)
}

func (offlineRuntime) PingClient(int, string) int { return -1 }

func (offlineRuntime) DisconnectClient(int) {}

func (offlineRuntime) ClientConnected(int) bool { return false }

func (offlineRuntime) RequestClientUpdate(int) (int, error) { return 0, ErrClientOffline }

func (offlineRuntime) DeleteClientResources(int) {}

func (offlineRuntime) GenerateTunnelPort(tunnel *model.Tunnel) int {
	return ports.Allowed{}.GenerateTunnelPort(tunnel)
}

func (offlineRuntime) TunnelPortAvailable(tunnel *model.Tunnel) bool {
	return ports.Allowed{}.TestTunnelPort(tunnel)
}

func (offlineRuntime) ListTunnels(int, int, string, int, string, string, string) ([]*model.Tunnel, int) {
	return nil, 0
}

func (offlineRuntime) AddTunnel(*model.Tunnel) error { return ErrServerNotRunning }

func (offlineRuntime) StopTunnel(int) error { return ErrServerNotRunning }

func (offlineRuntime) StartTunnel(int) error { return ErrServerNotRunning }

func (offlineRuntime) DeleteTunnel(int) error { return ErrServerNotRunning }

func (r offlineRuntime) ListHosts(offset, limit, clientID int, search, _, _ string) ([]*model.Host, int) {
	return r.db.GetHost(offset, limit, clientID, search)
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
