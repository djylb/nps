package proxy

import (
	"errors"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type Service interface {
	Start() error
	Close() error
}

type NetBridge interface {
	SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error)
	IsServer() bool
	CliProcess(c *conn.Conn, tunnelType string)
}

// BaseServer struct
type BaseServer struct {
	Id              int
	Bridge          NetBridge
	Task            *file.Tunnel
	ErrorContent    []byte
	AllowLocalProxy bool
	sync.Mutex
}

func NewBaseServer(bridge NetBridge, task *file.Tunnel, allowLocalProxy bool) *BaseServer {
	return &BaseServer{
		Bridge:          bridge,
		Task:            task,
		ErrorContent:    nil,
		AllowLocalProxy: allowLocalProxy,
		Mutex:           sync.Mutex{},
	}
}

// FlowAdd add the flow
func (s *BaseServer) FlowAdd(in, out int64) {
	s.Lock()
	defer s.Unlock()
	s.Task.Flow.ExportFlow += out
	s.Task.Flow.InletFlow += in
}

// FlowAddHost change the flow
func (s *BaseServer) FlowAddHost(host *file.Host, in, out int64) {
	s.Lock()
	defer s.Unlock()
	host.Flow.ExportFlow += out
	host.Flow.InletFlow += in
}

// write fail bytes to the connection
func (s *BaseServer) writeConnFail(c net.Conn) {
	_, _ = c.Write([]byte(common.ConnectionFailBytes))
	_, _ = c.Write(s.ErrorContent)
}

// Auth check
func (s *BaseServer) Auth(r *http.Request, c *conn.Conn, u, p string, multiAccount, userAuth *file.MultiAccount) error {
	if !common.CheckAuth(r, u, p, file.GetAccountMap(multiAccount), file.GetAccountMap(userAuth)) {
		if c != nil {
			_, _ = c.Write([]byte(common.UnauthorizedBytes))
			_ = c.Close()
		}
		return errors.New("401 Unauthorized")
	}
	return nil
}

// CheckFlowAndConnNum check flow limit of the client ,and decrease the allowed num of client
func (s *BaseServer) CheckFlowAndConnNum(client *file.Client) error {
	if !client.Flow.TimeLimit.IsZero() && client.Flow.TimeLimit.Before(time.Now()) {
		return errors.New("service access expired")
	}
	if client.Flow.FlowLimit > 0 && (client.Flow.FlowLimit<<20) < (client.Flow.ExportFlow+client.Flow.InletFlow) {
		return errors.New("traffic limit exceeded")
	}
	if !client.GetConn() {
		return errors.New("connection limit exceeded")
	}
	return nil
}

func in(target string, strArray []string) bool {
	sort.Strings(strArray)
	index := sort.SearchStrings(strArray, target)
	if index < len(strArray) && strArray[index] == target {
		return true
	}
	return false
}

func (s *BaseServer) DealClient(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel) error {

	if IsGlobalBlackIp(c.RemoteAddr().String()) || common.IsBlackIp(c.RemoteAddr().String(), client.VerifyKey, client.BlackIpList) {
		_ = c.Close()
		return nil
	}
	if task != nil && task.Mode == "mixProxy" && task.DestAclMode != file.AclOff {
		if !task.AllowsDestination(addr) {
			logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s",
				client.Id, task.Id, common.ExtractHost(addr))
			_ = c.Close()
			return errors.New("destination denied by dest acl")
		}
	}
	isLocal := s.AllowLocalProxy && localProxy || client.Id < 0
	link := conn.NewLink(tp, addr, client.Cnf.Crypt, client.Cnf.Compress, c.Conn.RemoteAddr().String(), isLocal)
	target, err := s.Bridge.SendLinkInfo(client.Id, link, s.Task)
	if err != nil {
		logs.Warn("get connection from client Id %d  error %v", client.Id, err)
		_ = c.Close()
		return err
	}

	if f != nil {
		f()
	}
	isFramed := tp == common.CONN_UDP
	conn.CopyWaitGroup(target, c.Conn, link.Crypt, link.Compress, client.Rate, flows, true, proxyProtocol, rb, task, isLocal, isFramed)
	return nil
}

func IsGlobalBlackIp(ipPort string) bool {
	global := file.GetDb().GetGlobal()
	if global != nil {
		ip := common.GetIpByAddr(ipPort)
		if in(ip, global.BlackIpList) {
			logs.Error("IP address [%s] is in the global blacklist", ip)
			return true
		}
	}

	return false
}
