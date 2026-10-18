package proxy

import (
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var errProxyAccessDenied = errors.New("proxy access denied")

// ErrServerClosed is returned by Bind and Start once the server was closed.
var ErrServerClosed = errors.New("server closed")

type Service interface {
	Start() error
	Close() error
}

// Binder is implemented by services that own a listening socket. Bind opens
// it synchronously so that the caller learns about address errors before the
// service is published, after which Start serves on it. Bind is idempotent and
// fails with ErrServerClosed once Close has been called, so a Close racing
// with Bind or Start never leaves a socket open.
type Binder interface {
	Bind() error
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

	lifecycleMu sync.Mutex
	closed      bool
}

// bindLocked runs bind under the lifecycle lock unless the server is closed.
func (s *BaseServer) bindLocked(bind func() error) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return ErrServerClosed
	}
	return bind()
}

// markClosed marks the server closed and runs snapshot under the lifecycle
// lock, so snapshot observes every resource a concurrent Bind opened. The
// resources should be released after markClosed returns.
func (s *BaseServer) markClosed(snapshot func()) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.closed = true
	snapshot()
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
	s.Task.Flow.Add(in, out)
}

// FlowAddHost change the flow
func (s *BaseServer) FlowAddHost(host *file.Host, in, out int64) {
	host.Flow.Add(in, out)
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
	traffic, expired := client.Flow.Over()
	if expired {
		return errors.New("service access expired")
	}
	if traffic {
		return errors.New("traffic limit exceeded")
	}
	if !client.GetConn() {
		return errors.New("connection limit exceeded")
	}
	return nil
}

func (s *BaseServer) DealClient(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel) error {
	target, link, isLocal, err := s.openClientLink(c, client, addr, tp, localProxy, task)
	if err != nil {
		_ = c.Close()
		return err
	}
	if target == nil || link == nil {
		_ = c.Close()
		return nil
	}

	if f != nil {
		f()
	}
	s.pipeClientConn(target, c, link, client, flows, proxyProtocol, rb, task, isLocal, tp)
	return nil
}

func (s *BaseServer) openClientLink(c *conn.Conn, client *file.Client, addr, tp string, localProxy bool, task *file.Tunnel) (net.Conn, *conn.Link, bool, error) {
	if SourceDenied(c.RemoteAddr(), client, taskLists(task), taskName(task)) {
		return nil, nil, false, errProxyAccessDenied
	}
	if task != nil && task.Mode == "mixProxy" && task.DestAclMode != file.AclOff {
		if !task.AllowsDestination(addr) {
			logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s",
				client.Id, task.Id, common.ExtractHost(addr))
			return nil, nil, false, errProxyAccessDenied
		}
	}
	isLocal := s.AllowLocalProxy && localProxy || client.Id < 0
	link := conn.NewLink(tp, addr, client.Cnf.Crypt, client.Cnf.Compress, c.Conn.RemoteAddr().String(), isLocal)
	target, err := s.Bridge.SendLinkInfo(client.Id, link, task)
	if err != nil {
		logs.Warn("get connection from client Id %d  error %v", client.Id, err)
		return nil, nil, false, err
	}
	return target, link, isLocal, nil
}

func (s *BaseServer) pipeClientConn(target net.Conn, c *conn.Conn, link *conn.Link, client *file.Client, flows []*file.Flow, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal bool, tp string) {
	isFramed := tp == common.ConnUDP
	conn.CopyWaitGroup(target, c.Conn, link.Crypt, link.Compress, client.Rate, flows, true, proxyProtocol, rb, task, isLocal, isFramed)
}
