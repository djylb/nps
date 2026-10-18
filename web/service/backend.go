package service

import (
	"errors"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/tool"
)

type Repository interface {
	RangeClients(func(*file.Client) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
	ListVisibleClients(ListClientsInput) ([]*file.Client, int)
	NextClientID() int
	NextTunnelID() int
	NextHostID() int
	CreateClient(*file.Client) error
	GetClient(int) (*file.Client, error)
	SaveClient(*file.Client) error
	PersistClients() error
	DeleteClient(int) error
	VerifyUserName(string, int) bool
	VerifyVKey(string, int) bool
	ReplaceClientVKeyIndex(oldKey, newKey string, clientID int)
	CreateTunnel(*file.Tunnel) error
	GetTunnel(int) (*file.Tunnel, error)
	SaveTunnel(*file.Tunnel) error
	DeleteTunnelRecord(int) error
	CreateHost(*file.Host) error
	GetHost(int) (*file.Host, error)
	SaveHost(*file.Host, string) error
	PersistHosts() error
	DeleteHostRecord(int) error
	HostExists(*file.Host) bool
	GetGlobal() *file.Glob
	SaveGlobal(*file.Glob) error
	ClientOwnsTunnel(int, int) bool
	ClientOwnsHost(int, int) bool
}

type Runtime interface {
	DashboardData(bool) map[string]interface{}
	ListClients(offset, limit int, search, sort, order string, clientID int) ([]*file.Client, int)
	PingClient(int, string) int
	DisconnectClient(int)
	RequestClientUpdate(int) (int, error)
	DeleteClientResources(int)
	GenerateTunnelPort(*file.Tunnel) int
	TunnelPortAvailable(*file.Tunnel) bool
	ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int)
	AddTunnel(*file.Tunnel) error
	StopTunnel(int) error
	StartTunnel(int) error
	DeleteTunnel(int) error
	ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int)
	RemoveHostCache(int)
}

type Backend struct {
	Repository Repository
	Runtime    Runtime
}

func DefaultBackend() Backend {
	return Backend{
		Repository: defaultRepository{},
		Runtime:    defaultRuntime{},
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

type defaultRuntime struct{}

func (defaultRepository) RangeClients(fn func(*file.Client) bool) {
	file.GetDb().JsonDb.Clients.Range(func(_, value interface{}) bool {
		return fn(value.(*file.Client))
	})
}

func (defaultRepository) RangeTunnels(fn func(*file.Tunnel) bool) {
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		return fn(value.(*file.Tunnel))
	})
}

func (defaultRepository) RangeHosts(fn func(*file.Host) bool) {
	file.GetDb().JsonDb.Hosts.Range(func(_, value interface{}) bool {
		return fn(value.(*file.Host))
	})
}

func (defaultRepository) ListVisibleClients(input ListClientsInput) ([]*file.Client, int) {
	if input.Visibility.IsAdmin {
		clientID := visibleClientID(input.Visibility)
		return server.GetClientList(input.Offset, input.Limit, input.Search, input.Sort, input.Order, clientID)
	}

	visible := visibleClientSet(input.Visibility)
	if len(visible) == 0 {
		return nil, 0
	}
	if len(visible) == 1 {
		return server.GetClientList(input.Offset, input.Limit, input.Search, input.Sort, input.Order, visibleClientID(input.Visibility))
	}

	list := make([]*file.Client, 0)
	var count int
	originLimit := input.Limit
	searchID := common.GetIntNoErrByStr(input.Search)
	keys := file.GetMapKeys(&file.GetDb().JsonDb.Clients, true, input.Sort, input.Order)
	for _, key := range keys {
		value, ok := file.GetDb().JsonDb.Clients.Load(key)
		if !ok {
			continue
		}
		client := value.(*file.Client)
		if client.NoDisplay {
			continue
		}
		if _, ok := visible[client.Id]; !ok {
			continue
		}
		if input.Search != "" &&
			client.Id != searchID &&
			!common.ContainsFold(client.VerifyKey, input.Search) &&
			!common.ContainsFold(client.Remark, input.Search) {
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
	return int(file.GetDb().JsonDb.GetTaskId())
}

func (defaultRepository) NextHostID() int {
	return int(file.GetDb().JsonDb.GetHostId())
}

func (defaultRepository) CreateClient(client *file.Client) error {
	return file.GetDb().NewClient(client)
}

func (defaultRepository) GetClient(id int) (*file.Client, error) {
	return file.GetDb().GetClient(id)
}

func (defaultRepository) SaveClient(_ *file.Client) error {
	return file.GetDb().JsonDb.StoreClientsToJsonFile()
}

func (defaultRepository) PersistClients() error {
	return file.GetDb().JsonDb.StoreClientsToJsonFile()
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
	file.Blake2bVkeyIndex.Remove(crypt.Blake2b(oldKey))
	file.Blake2bVkeyIndex.Add(crypt.Blake2b(newKey), clientID)
}

func (defaultRepository) CreateTunnel(tunnel *file.Tunnel) error {
	return file.GetDb().NewTask(tunnel)
}

func (defaultRepository) GetTunnel(id int) (*file.Tunnel, error) {
	return file.GetDb().GetTask(id)
}

func (defaultRepository) SaveTunnel(tunnel *file.Tunnel) error {
	return file.GetDb().UpdateTask(tunnel)
}

func (defaultRepository) DeleteTunnelRecord(id int) error {
	return file.GetDb().DelTask(id)
}

func (defaultRepository) CreateHost(host *file.Host) error {
	return file.GetDb().NewHost(host)
}

func (defaultRepository) GetHost(id int) (*file.Host, error) {
	return file.GetDb().GetHostById(id)
}

func (defaultRepository) SaveHost(host *file.Host, oldHost string) error {
	if oldHost != "" && oldHost != host.Host {
		file.HostIndex.Remove(oldHost, host.Id)
		file.HostIndex.Add(host.Host, host.Id)
	}
	return file.GetDb().JsonDb.StoreHostToJsonFile()
}

func (defaultRepository) PersistHosts() error {
	return file.GetDb().JsonDb.StoreHostToJsonFile()
}

func (defaultRepository) DeleteHostRecord(id int) error {
	return file.GetDb().DelHost(id)
}

func (defaultRepository) HostExists(host *file.Host) bool {
	return file.GetDb().IsHostExist(host)
}

func (defaultRepository) GetGlobal() *file.Glob {
	return file.GetDb().GetGlobal()
}

func (defaultRepository) SaveGlobal(glob *file.Glob) error {
	return file.GetDb().SaveGlobal(glob)
}

func (defaultRepository) ClientOwnsTunnel(clientID, tunnelID int) bool {
	if clientID <= 0 || tunnelID <= 0 {
		return false
	}
	if v, ok := file.GetDb().JsonDb.Tasks.Load(tunnelID); ok {
		return v.(*file.Tunnel).Client.Id == clientID
	}
	return false
}

func (defaultRepository) ClientOwnsHost(clientID, hostID int) bool {
	if clientID <= 0 || hostID <= 0 {
		return false
	}
	if v, ok := file.GetDb().JsonDb.Hosts.Load(hostID); ok {
		return v.(*file.Host).Client.Id == clientID
	}
	return false
}

func (defaultRuntime) DashboardData(force bool) map[string]interface{} {
	return server.GetDashboardData(force)
}

func (defaultRuntime) ListClients(offset, limit int, search, sort, order string, clientID int) ([]*file.Client, int) {
	return server.GetClientList(offset, limit, search, sort, order, clientID)
}

func (defaultRuntime) PingClient(id int, remoteAddr string) int {
	return server.PingClient(id, remoteAddr)
}

func (defaultRuntime) DisconnectClient(id int) {
	server.DelClientConnect(id)
}

func (defaultRuntime) RequestClientUpdate(id int) (int, error) {
	n, err := server.RequestClientUpdate(id)
	switch {
	case errors.Is(err, bridge.ErrClientOffline):
		err = ErrClientOffline
	case errors.Is(err, bridge.ErrUpdateUnsupported):
		err = ErrClientUpdateUnsupported
	}
	return n, err
}

func (defaultRuntime) DeleteClientResources(id int) {
	server.DelTunnelAndHostByClientId(id, false)
}

func (defaultRuntime) GenerateTunnelPort(tunnel *file.Tunnel) int {
	return tool.GenerateTunnelPort(tunnel)
}

func (defaultRuntime) TunnelPortAvailable(tunnel *file.Tunnel) bool {
	return tool.TestTunnelPort(tunnel)
}

func (defaultRuntime) ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int) {
	return server.GetTunnel(offset, limit, tunnelType, clientID, search, sort, order)
}

func (defaultRuntime) AddTunnel(tunnel *file.Tunnel) error {
	return server.AddTask(tunnel)
}

func (defaultRuntime) StopTunnel(id int) error {
	return server.StopServer(id)
}

func (defaultRuntime) StartTunnel(id int) error {
	return server.StartTask(id)
}

func (defaultRuntime) DeleteTunnel(id int) error {
	return server.DelTask(id)
}

func (defaultRuntime) ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int) {
	return server.GetHostList(offset, limit, clientID, search, sort, order)
}

func (defaultRuntime) RemoveHostCache(id int) {
	server.HttpProxyCache.Remove(id)
}
