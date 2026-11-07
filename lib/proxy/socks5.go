package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/socks5"
	"github.com/djylb/nps/lib/auth"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/slogx"
)

// newSocks5Server returns the SOCKS5 server of s. Bind sets its UDP socket.
func newSocks5Server(s *TunnelModeServer) *socks5.Server {
	return &socks5.Server{
		Handle:         s.handleSocks5,
		UDPAddr:        s.socks5UDPAddr,
		DialPacket:     s.dialSocks5Packet,
		AllowPacket:    s.allowsSocks5Datagram,
		UDPQueue:       socks5UDPQueue,
		UDPIdleTimeout: socks5UDPIdleTimeout,
		OnError: func(c net.Conn, err error) {
			log.Debug("socks5 request failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
		},
	}
}

// handleSocks5 serves CONNECT through the client tunnel and leaves the other
// commands to the server.
func (s *TunnelModeServer) handleSocks5(_ context.Context, req *socks5.Request) (bool, error) {
	if req.Command != socks5.CmdConnect {
		return false, nil
	}
	targetAddr := req.Dst.String()
	// clientConn wraps req.Conn only, which every path below closes or relays.
	//noinspection GoResourceLeak
	clientConn := conn.NewConn(req.Conn)
	target, link, isLocal, err := s.openClientLink(clientConn, s.Task.Client, targetAddr, conn.LinkTCP, s.Task.Target.LocalProxy, s.Task.Route().Node, s.Task)
	if err != nil {
		rep := socks5.ReplyGeneralFailure
		if errors.Is(err, errProxyAccessDenied) {
			rep = socks5.ReplyNotAllowed
		}
		_ = req.Reply(rep, netx.Addr{})
		return true, err
	}
	if target == nil || link == nil {
		return true, nil
	}
	if err := req.Reply(socks5.ReplySucceeded, socks5ConnectReplyAddr(s.Task, req.Conn)); err != nil {
		_ = target.Close()
		return true, err
	}
	s.pipeClientConn(target, clientConn, link, s.Task.Client, []conn.Flow{s.Task.Flow, s.Task.Client.Flow}, 0, nil, s.Task, isLocal, conn.LinkTCP)
	return true, nil
}

// socks5Auth returns the authenticators for the task's current accounts:
// username/password when it has any, else none.
func (s *TunnelModeServer) socks5Auth() []socks5.Authenticator {
	if !socks5NeedsAuth(s.Task) {
		return nil
	}
	return []socks5.Authenticator{socks5.UserPassAuth{Check: func(_ context.Context, c net.Conn, username, password string) (bool, error) {
		user, passwd := socks5ClientCredentials(s.Task)
		if !auth.CheckAuthWithAccountMap(username, password, user, passwd,
			model.GetAccountMap(s.Task.MultiAccount), model.GetAccountMap(s.Task.UserAuth)) {
			proxyAuthFailed(c.RemoteAddr(), s.Task, username)
			return false, nil
		}
		return true, nil
	}}}
}

func ProcessMix(c *conn.Conn, s *TunnelModeServer) error {
	// the source lists apply before any handshake, so that denied
	// addresses cannot try the proxy credentials
	if s.Bridge.IsServer() && SourceDenied(c.RemoteAddr(), s.Bridge.GlobalSource(), s.Task.Client, &s.Task.SourceLists, taskName(s.Task)) {
		_ = c.Close()
		return errProxyAccessDenied
	}
	httpEnabled, socksEnabled := effectiveMixProxyFlags(s.Task)

	// the socks5 handshake and the http path set their own deadlines
	_ = c.SetReadDeadline(time.Now().Add(socks5.DefaultHandshakeTimeout))
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		log.Debug("mixed proxy negotiation failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
		_ = c.Close()
		return err
	}

	if header[0] != socks5.Version5 {
		if looksLikeHTTPProxyRequest(header[:]) {
			if !httpEnabled {
				slogx.Emit(log, &logevent.TunnelDenied, "src_ip", srcIP(c.RemoteAddr()), "route", taskName(s.Task), "reason", "http_disabled")
				_ = c.Close()
				return errors.New("http proxy is disabled")
			}
			if err := ProcessHttp(netx.NewPrefixConn(c, header[:]), s); err != nil {
				log.Debug("http proxy failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
				_ = c.Close()
				return err
			}
			_ = c.Close()
			return nil
		}
		log.Debug("unknown proxy protocol", "src_ip", srcIP(c.RemoteAddr()), "head", header[:])
		_ = c.Close()
		return errors.New("unknown protocol")
	}

	if !socksEnabled {
		slogx.Emit(log, &logevent.TunnelDenied, "src_ip", srcIP(c.RemoteAddr()), "route", taskName(s.Task), "reason", "socks5_disabled")
		_ = c.Close()
		return errors.New("socks5 proxy is disabled")
	}

	// The accounts are read per connection, as they may change while the
	// task runs.
	hs := &socks5.Server{Auth: s.socks5Auth()}
	req, err := hs.Handshake(context.Background(), netx.NewPrefixConn(c, header[:]))
	if err != nil {
		log.Debug("socks5 negotiation failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
		return err
	}
	if err := s.socks5.ServeRequest(context.Background(), req); err != nil {
		log.Debug("socks5 request failed", "src_ip", srcIP(c.RemoteAddr()), "err", err)
	}
	return nil
}

func effectiveMixProxyFlags(task *model.Tunnel) (httpEnabled, socksEnabled bool) {
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

func socks5NeedsAuth(task *model.Tunnel) bool {
	if task == nil {
		return false
	}
	user, passwd := socks5ClientCredentials(task)
	return auth.NeedsAuth(user, passwd,
		model.GetAccountMap(task.MultiAccount), model.GetAccountMap(task.UserAuth))
}

func socks5ClientCredentials(task *model.Tunnel) (user, passwd string) {
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
