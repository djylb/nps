package proxy

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/transport"
)

const (
	ipV4            = 1
	domainName      = 3
	ipV6            = 4
	connectMethod   = 1
	bindMethod      = 2
	associateMethod = 3

	socks5AssociateControlPollInterval = time.Second

	// socks5HandshakeTimeout bounds reading the greeting, credentials and
	// request, so that a slow or silent client cannot hold the connection.
	socks5HandshakeTimeout = 10 * time.Second
)

// SOCKS5 reply codes (RFC 1928 section 6).
const (
	succeeded            uint8 = 0
	serverFailure        uint8 = 1
	notAllowed           uint8 = 2
	commandNotSupported  uint8 = 7
	addrTypeNotSupported uint8 = 8
)

const (
	noAuthMethod     = uint8(0)
	UserPassAuth     = uint8(2)
	noAcceptableAuth = uint8(0xFF)

	userAuthVersion = uint8(1)
	authSuccess     = uint8(0)
	authFailure     = uint8(1)
)

func (s *TunnelModeServer) handleSocks5Request(c net.Conn) {
	request, err := readSocks5Request(c)
	if err != nil {
		replyCode := serverFailure
		if errors.Is(err, errSocks5UnsupportedAddrType) {
			replyCode = addrTypeNotSupported
		}
		logs.Warn("invalid socks5 request from %v: %v", c.RemoteAddr(), err)
		_ = writeSocks5Reply(c, replyCode, socks5ConnReplyAddr(c))
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	switch request.Command {
	case connectMethod:
		s.handleSocks5Connect(c, request.Destination)
	case bindMethod:
		_ = writeSocks5Reply(c, commandNotSupported, socks5ConnReplyAddr(c))
		_ = c.Close()
	case associateMethod:
		s.handleSocks5Associate(c, request.Destination)
	default:
		_ = writeSocks5Reply(c, commandNotSupported, socks5ConnReplyAddr(c))
		_ = c.Close()
	}
}

func (s *TunnelModeServer) handleSocks5Connect(c net.Conn, dest socks5Address) {
	targetAddr := dest.String()
	if s.Task != nil && s.Task.Mode == "mixProxy" && s.Task.DestAclMode != file.AclOff && !s.Task.AllowsDestination(targetAddr) {
		logs.Warn("mixProxy dest acl deny: client=%d task=%d dest=%s", s.Task.Client.Id, s.Task.Id, common.ExtractHost(targetAddr))
		_ = writeSocks5Reply(c, notAllowed, socks5ConnReplyAddr(c))
		_ = c.Close()
		return
	}

	// clientConn wraps c only, which every path below closes or relays.
	//noinspection GoResourceLeak
	clientConn := conn.NewConn(c)
	target, link, isLocal, err := s.openClientLink(clientConn, s.Task.Client, targetAddr, common.ConnTCP, s.Task.Target.LocalProxy, s.Task)
	if err != nil {
		logs.Warn("open socks5 connect tunnel failed: client=%d task=%d dest=%s err=%v", s.Task.Client.Id, s.Task.Id, targetAddr, err)
		replyCode := serverFailure
		if errors.Is(err, errProxyAccessDenied) {
			replyCode = notAllowed
		}
		_ = writeSocks5Reply(c, replyCode, socks5ConnReplyAddr(c))
		_ = c.Close()
		return
	}
	if target == nil || link == nil {
		_ = c.Close()
		return
	}

	replyAddr := socks5ConnectReplyAddr(s.Task, c)
	if err := writeSocks5Reply(c, succeeded, replyAddr); err != nil {
		_ = target.Close()
		_ = c.Close()
		return
	}
	s.pipeClientConn(target, clientConn, link, s.Task.Client, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, nil, s.Task, isLocal, common.ConnTCP)
}

func (s *TunnelModeServer) handleSocks5Associate(c net.Conn, dest socks5Address) {
	if tcpConn, ok := c.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(15 * time.Second)
		_ = transport.SetTcpKeepAliveParams(tcpConn, 15, 15, 3)
	}
	if s.Task != nil && s.Task.Mode == "mixProxy" && s.Task.DestAclMode != file.AclOff {
		logs.Warn("mixProxy dest acl active, reject socks5 udp associate: client=%d task=%d", s.Task.Client.Id, s.Task.Id)
		_ = writeSocks5Reply(c, notAllowed, socks5ConnReplyAddr(c))
		_ = c.Close()
		return
	}
	defer func() { _ = c.Close() }()

	logs.Trace("ASSOCIATE %s", dest.String())
	if s.socks5UDP == nil {
		logs.Error("socks5 udp listener is unavailable on task %d", s.Task.Id)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		return
	}

	clientIP := common.NormalizeIP(common.ParseIPFromAddr(c.RemoteAddr().String()))
	session, err := s.socks5UDP.newSession(c, int(dest.Port))
	if err != nil {
		logs.Warn("open socks5 udp associate tunnel failed: client=%d task=%d err=%v", s.Task.Client.Id, s.Task.Id, err)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		return
	}
	defer session.Close()

	if err := writeSocks5Reply(c, succeeded, socks5UDPReplyAddr(s.Task, c, s.socks5UDP.listener, clientIP)); err != nil {
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

func (s *TunnelModeServer) SocksAuth(c net.Conn) error {
	username, password, err := readSocks5UserPassCredentials(c)
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
	status := authFailure
	if valid {
		status = authSuccess
	}
	if _, err := c.Write([]byte{userAuthVersion, status}); err != nil {
		return err
	}
	if !valid {
		return errors.New("auth failed")
	}
	return nil
}

func ProcessMix(c *conn.Conn, s *TunnelModeServer) error {
	httpEnabled, socksEnabled := effectiveMixProxyFlags(s.Task)

	// cleared once the socks5 request is read; the http path sets its own
	_ = c.SetReadDeadline(time.Now().Add(socks5HandshakeTimeout))
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		logs.Warn("negotiation err %v", err)
		_ = c.Close()
		return err
	}

	if header[0] != 5 {
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

	methods, err := readSocks5Methods(c, int(header[1]))
	if err != nil {
		logs.Warn("wrong method")
		_ = c.Close()
		return errors.New("wrong method")
	}

	if err := s.negotiateSocks5Method(c, methods); err != nil {
		_ = c.Close()
		return err
	}
	s.handleSocks5Request(c)
	return nil
}

func (s *TunnelModeServer) negotiateSocks5Method(c net.Conn, methods []byte) error {
	supports := func(method byte) bool {
		for _, offered := range methods {
			if offered == method {
				return true
			}
		}
		return false
	}

	if socks5NeedsAuth(s.Task) {
		if !supports(UserPassAuth) {
			_, _ = c.Write([]byte{5, noAcceptableAuth})
			return errors.New("no acceptable authentication method")
		}
		if _, err := c.Write([]byte{5, UserPassAuth}); err != nil {
			return err
		}
		if err := s.SocksAuth(c); err != nil {
			logs.Warn("Validation failed: %v", err)
			return err
		}
		return nil
	}

	if len(methods) == 0 {
		_, _ = c.Write([]byte{5, noAuthMethod})
		return nil
	}

	if !supports(noAuthMethod) {
		_, _ = c.Write([]byte{5, noAcceptableAuth})
		return errors.New("no acceptable method (no-auth not offered)")
	}
	_, _ = c.Write([]byte{5, noAuthMethod})
	return nil
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
