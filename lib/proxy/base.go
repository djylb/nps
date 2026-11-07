package proxy

import (
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/byterate"
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
	// Session returns the way to open links to client clientId.
	Session(clientId int) conn.ClientSession
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

// BindLocked runs bind under the lifecycle lock unless the server is closed.
func (s *BaseServer) BindLocked(bind func() error) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return ErrServerClosed
	}
	return bind()
}

// MarkClosed marks the server closed and runs snapshot under the lifecycle
// lock, so snapshot observes every resource a concurrent Bind opened. The
// resources should be released after MarkClosed returns.
func (s *BaseServer) MarkClosed(snapshot func()) {
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

// AcquireForwarding admits once at a leaf. byterate reserves every ancestor
// atomically and rolls all slots back if any cap rejects the transfer.
func (s *BaseServer) AcquireForwarding(client *file.Client, group *byterate.Group) (func(), error) {
	traffic, expired := client.Flow.Over()
	if expired {
		return nil, errors.New("service access expired")
	}
	if traffic {
		return nil, errors.New("traffic limit exceeded")
	}
	if group == nil {
		group = client.ControlGroup()
	}
	if err := group.Acquire(); err != nil {
		return nil, err
	}
	return sync.OnceFunc(group.Release), nil
}

func (s *BaseServer) DealClient(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []conn.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel) error {
	var node string
	if task != nil {
		node = task.Route().Node
	}
	target, link, isLocal, err := s.openClientLink(c, client, addr, tp, localProxy, node, task)
	if err != nil {
		_ = c.Close()
		return err
	}
	return s.relayClient(target, c, link, isLocal, client, rb, tp, f, flows, proxyProtocol, task)
}

// DealTarget is DealClient along a route of task, to one of the addresses of
// its target, retrying the
// next address if the server fails to dial one itself. It returns
// file.ErrNoTarget, leaving c open, if no address can be tried.
func (s *BaseServer) DealTarget(c *conn.Conn, client *file.Client,
	rb []byte, tp string, f func(), flows []conn.Flow, task *file.Tunnel) error {
	var target net.Conn
	var link *conn.Link
	var isLocal bool
	route := task.Route()
	dest := route.Target
	err := dest.Dial(func(addr string) (bool, error) {
		var err error
		target, link, isLocal, err = s.openClientLink(c, client, addr, tp, dest.LocalProxy, route.Node, task)
		return isLocal, err
	})
	if errors.Is(err, file.ErrNoTarget) {
		return err
	}
	if err != nil {
		_ = c.Close()
		return err
	}
	return s.relayClient(target, c, link, isLocal, client, rb, tp, f, flows, dest.ProxyProtocol, task)
}

func (s *BaseServer) relayClient(target net.Conn, c *conn.Conn, link *conn.Link, isLocal bool, client *file.Client,
	rb []byte, tp string, f func(), flows []conn.Flow, proxyProtocol int, task *file.Tunnel) error {
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

func (s *BaseServer) openClientLink(c *conn.Conn, client *file.Client, addr, tp string, localProxy bool, node string, task *file.Tunnel) (net.Conn, *conn.Link, bool, error) {
	if s.Bridge.IsServer() && SourceDenied(c.RemoteAddr(), client, taskLists(task), taskName(task)) {
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
	link.Option.Node = node
	taskID := 0
	if task != nil {
		taskID = task.Id
	}
	target, err := s.Bridge.Session(client.Id).OpenLink(link, taskID)
	if err != nil {
		logs.Warn("get connection from client Id %d  error %v", client.Id, err)
		return nil, nil, isLocal, err
	}
	return target, link, isLocal, nil
}

func (s *BaseServer) pipeClientConn(target net.Conn, c *conn.Conn, link *conn.Link, client *file.Client, flows []conn.Flow, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal bool, tp string) {
	isFramed := tp == common.ConnUDP
	var entry conn.Entry
	if task != nil {
		entry = task
	}
	conn.CopyWaitGroup(target, c.Conn, link.Crypt, link.Compress, client.Rate, flows, true, proxyProtocol, rb, entry, isLocal, isFramed)
}
