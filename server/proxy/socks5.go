package proxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"slices"
	"time"

	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

const (
	socks5AssociateControlPollInterval = time.Second

	// socks5HandshakeTimeout bounds reading the greeting, credentials and
	// request, so that a slow or silent client cannot hold the connection.
	socks5HandshakeTimeout = 10 * time.Second
)

func (s *TunnelModeServer) handleSocks5Request(c net.Conn) {
	cmd, dst, err := socks5.ReadRequest(c)
	if err != nil {
		logs.Warn("invalid socks5 request from %v: %v", c.RemoteAddr(), err)
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			rep := socks5.ReplyGeneralFailure
			if errors.Is(err, socks5.ErrAddrType) {
				rep = socks5.ReplyAddrTypeNotSupported
			}
			_ = socks5.WriteReply(c, rep, socks5.Addr{})
		}
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	switch cmd {
	case socks5.CmdConnect:
		s.handleSocks5Connect(c, dst)
	case socks5.CmdUDPAssociate:
		s.handleSocks5Associate(c, dst)
	default:
		_ = socks5.WriteReply(c, socks5.ReplyCommandNotSupported, socks5.Addr{})
		_ = c.Close()
	}
}

func (s *TunnelModeServer) handleSocks5Connect(c net.Conn, dst socks5.Addr) {
	targetAddr := dst.String()
	if s.Task != nil && s.Task.Mode == "mixProxy" && s.Task.DestAclMode != file.AclOff && !s.Task.AllowsDestination(targetAddr) {
		logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s", s.Task.Client.Id, s.Task.Id, common.ExtractHost(targetAddr))
		_ = socks5.WriteReply(c, socks5.ReplyNotAllowed, socks5.Addr{})
		_ = c.Close()
		return
	}

	// clientConn wraps c only, which every path below closes or relays.
	//noinspection GoResourceLeak
	clientConn := conn.NewConn(c)
	target, link, isLocal, err := s.openClientLink(clientConn, s.Task.Client, targetAddr, common.ConnTCP, s.Task.Target.LocalProxy, s.Task)
	if err != nil {
		logs.Warn("open socks5 connect tunnel failed: client=%d task=%d dest=%s err=%v", s.Task.Client.Id, s.Task.Id, targetAddr, err)
		rep := socks5.ReplyGeneralFailure
		if errors.Is(err, errProxyAccessDenied) {
			rep = socks5.ReplyNotAllowed
		}
		_ = socks5.WriteReply(c, rep, socks5.Addr{})
		_ = c.Close()
		return
	}
	if target == nil || link == nil {
		_ = c.Close()
		return
	}

	replyAddr := socks5ConnectReplyAddr(s.Task, c)
	if err := socks5.WriteReply(c, socks5.ReplySucceeded, replyAddr); err != nil {
		_ = target.Close()
		_ = c.Close()
		return
	}
	s.pipeClientConn(target, clientConn, link, s.Task.Client, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, nil, s.Task, isLocal, common.ConnTCP)
}

func (s *TunnelModeServer) handleSocks5Associate(c net.Conn, dst socks5.Addr) {
	if tcpConn, ok := c.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
			Enable:   true,
			Idle:     15 * time.Second,
			Interval: 15 * time.Second,
			Count:    3,
		})
	}
	defer func() { _ = c.Close() }()

	logs.Trace("ASSOCIATE %s", dst)
	if s.socks5UDP == nil {
		logs.Error("socks5 udp listener is unavailable on task %d", s.Task.Id)
		_ = socks5.WriteReply(c, socks5.ReplyGeneralFailure, socks5.Addr{})
		return
	}

	clientIP := common.NormalizeIP(common.ParseIPFromAddr(c.RemoteAddr().String()))
	session, err := s.socks5UDP.newSession(c, int(dst.Port))
	if err != nil {
		logs.Warn("open socks5 udp associate tunnel failed: client=%d task=%d err=%v", s.Task.Client.Id, s.Task.Id, err)
		_ = socks5.WriteReply(c, socks5.ReplyGeneralFailure, socks5.Addr{})
		return
	}
	defer session.Close()

	if err := socks5.WriteReply(c, socks5.ReplySucceeded, socks5UDPReplyAddr(s.Task, c, s.socks5UDP.listener, clientIP)); err != nil {
		return
	}
	session.start()

	s.waitForSocks5AssociateControl(c, session)
}

func (s *TunnelModeServer) waitForSocks5AssociateControl(c net.Conn, session *socks5UDPSession) {
	var buf [1]byte

	for {
		if err := c.SetReadDeadline(time.Now().Add(socks5AssociateControlPollInterval)); err != nil {
			logs.Debug("set socks5 associate control deadline error: %v", err)
		}
		if _, err := c.Read(buf[:]); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-session.Done():
					session.Wait()
					return
				default:
				}
				continue
			}
			session.Close()
			session.Wait()
			return
		}
	}
}

// SocksAuth reads the client's username/password request, checks it against
// the task's accounts and answers it. It returns socks5.ErrAuthFailed for
// rejected credentials.
func (s *TunnelModeServer) SocksAuth(c net.Conn) error {
	username, password, err := socks5.ReadUserPass(c)
	if err != nil {
		return err
	}

	user, passwd := socks5ClientCredentials(s.Task)
	valid := common.CheckAuthWithAccountMap(
		username,
		password,
		user,
		passwd,
		file.GetAccountMap(s.Task.MultiAccount),
		file.GetAccountMap(s.Task.UserAuth),
	)
	if err := socks5.WriteUserPassStatus(c, valid); err != nil {
		return err
	}
	if !valid {
		return socks5.ErrAuthFailed
	}
	return nil
}

func ProcessMix(c *conn.Conn, s *TunnelModeServer) error {
	// the source lists apply before any handshake, so that denied
	// addresses cannot try the proxy credentials
	if SourceDenied(c.RemoteAddr(), s.Task.Client, &s.Task.SourceLists, taskName(s.Task)) {
		_ = c.Close()
		return errProxyAccessDenied
	}
	httpEnabled, socksEnabled := effectiveMixProxyFlags(s.Task)

	// cleared once the socks5 request is read; the http path sets its own
	_ = c.SetReadDeadline(time.Now().Add(socks5HandshakeTimeout))
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		logs.Warn("negotiation err %v", err)
		_ = c.Close()
		return err
	}

	if header[0] != socks5.Version5 {
		if looksLikeHTTPProxyRequest(header[:]) {
			if !httpEnabled {
				logs.Warn("http proxy is disable, client %d request from: %v", s.Task.Client.Id, c.RemoteAddr())
				_ = c.Close()
				return errors.New("http proxy is disabled")
			}
			if err := ProcessHttp(c.SetRb(header[:]), s); err != nil {
				logs.Warn("http proxy error: %v", err)
				_ = c.Close()
				return err
			}
			_ = c.Close()
			return nil
		}
		logs.Trace("Socks5 Buf: %s", header[:])
		logs.Warn("only support socks5 and http, request from: %v", c.RemoteAddr())
		_ = c.Close()
		return errors.New("unknown protocol")
	}

	if !socksEnabled {
		logs.Warn("socks5 proxy is disable, client %d request from: %v", s.Task.Client.Id, c.RemoteAddr())
		_ = c.Close()
		return errors.New("socks5 proxy is disabled")
	}

	methods, err := socks5.ReadMethods(io.MultiReader(bytes.NewReader(header[:]), c))
	if err != nil {
		logs.Warn("read socks5 methods from %v: %v", c.RemoteAddr(), err)
		_ = c.Close()
		return err
	}

	if err := s.negotiateSocks5Method(c, methods); err != nil {
		logs.Warn("socks5 negotiation with %v failed: %v", c.RemoteAddr(), err)
		_ = c.Close()
		return err
	}
	s.handleSocks5Request(c)
	return nil
}

// negotiateSocks5Method selects an authentication method from those the
// client offers and runs it. With authentication required only
// username/password is accepted. Otherwise no authentication is selected,
// also for clients that offer no method or only unknown ones, and a client
// that offers username/password alone has any credentials accepted.
func (s *TunnelModeServer) negotiateSocks5Method(c net.Conn, methods []socks5.Method) error {
	if socks5NeedsAuth(s.Task) {
		if !slices.Contains(methods, socks5.MethodUserPass) {
			_ = socks5.WriteMethod(c, socks5.MethodNoAcceptable)
			return socks5.ErrNoAcceptableMethod
		}
		if err := socks5.WriteMethod(c, socks5.MethodUserPass); err != nil {
			return err
		}
		return s.SocksAuth(c)
	}

	if slices.Contains(methods, socks5.MethodUserPass) && !slices.Contains(methods, socks5.MethodNoAuth) {
		if err := socks5.WriteMethod(c, socks5.MethodUserPass); err != nil {
			return err
		}
		if _, _, err := socks5.ReadUserPass(c); err != nil {
			return err
		}
		return socks5.WriteUserPassStatus(c, true)
	}
	return socks5.WriteMethod(c, socks5.MethodNoAuth)
}

func effectiveMixProxyFlags(task *file.Tunnel) (httpEnabled, socksEnabled bool) {
	if task == nil {
		return false, false
	}
	switch task.Mode {
	case "socks5":
		return false, true
	case "httpProxy":
		return true, false
	case "mixProxy":
		return task.HttpProxy, task.Socks5Proxy
	default:
		return false, false
	}
}

func socks5NeedsAuth(task *file.Tunnel) bool {
	if task == nil {
		return false
	}
	user, passwd := socks5ClientCredentials(task)
	return common.NeedsAuth(user, passwd,
		file.GetAccountMap(task.MultiAccount), file.GetAccountMap(task.UserAuth))
}

func socks5ClientCredentials(task *file.Tunnel) (user, passwd string) {
	if task.Client == nil || task.Client.Cnf == nil {
		return "", ""
	}
	return task.Client.Cnf.U, task.Client.Cnf.P
}

func looksLikeHTTPProxyRequest(prefix []byte) bool {
	switch string(prefix) {
	case "GE", "PO", "HE", "PU", "DE", "OP", "CO", "TR", "PA", "PR", "MK", "MO", "LO", "UN", "RE", "AC", "SE", "LI":
		return true
	default:
		return false
	}
}
