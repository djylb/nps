package proxy

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
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
	// The maximum packet size of any udp Associate packet, based on ethernet's max size,
	// minus the IP and UDP headers. IPv4 has a 20 byte header, UDP adds an
	// additional 4 bytes.  This is a total overhead of 24 bytes.  Ethernet's
	// max packet size is 1500 bytes,  1500 - 24 = 1476.
	maxUDPPacketSize = 1476
)

const (
	succeeded uint8 = iota
	serverFailure
	notAllowed
	networkUnreachable
	hostUnreachable
	connectionRefused
	ttlExpired
	commandNotSupported
	addrTypeNotSupported
)

const (
	UserPassAuth    = uint8(2)
	userAuthVersion = uint8(1)
	authSuccess     = uint8(0)
	authFailure     = uint8(1)
)

//type Sock5ModeServer struct {
//	BaseServer
//	listener net.Listener
//}

// req
func (s *TunnelModeServer) handleSocks5Request(c net.Conn) {
	/*
		The SOCKS request is formed as follows:
		+----+-----+-------+------+----------+----------+
		|VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
		+----+-----+-------+------+----------+----------+
		| 1  |  1  | X'00' |  1   | Variable |    2     |
		+----+-----+-------+------+----------+----------+
	*/
	header := make([]byte, 3)

	_, err := io.ReadFull(c, header)

	if err != nil {
		logs.Warn("illegal request %v", err)
		_ = c.Close()
		return
	}

	switch header[1] {
	case connectMethod:
		s.handleConnect(c)
	case bindMethod:
		s.handleBind(c)
	case associateMethod:
		s.handleUDP(c)
	default:
		s.sendReply(c, commandNotSupported)
		_ = c.Close()
	}
}

// reply
func (s *TunnelModeServer) sendReply(c net.Conn, rep uint8) {
	reply := []byte{
		5,
		rep,
		0,
		1,
	}

	localAddr := c.LocalAddr().String()
	localHost, localPort, _ := net.SplitHostPort(localAddr)
	ipBytes := net.ParseIP(localHost).To4()
	if ipBytes == nil {
		ipBytes = net.ParseIP("127.0.0.1").To4()
	}
	nPort, _ := strconv.Atoi(localPort)
	reply = append(reply, ipBytes...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(nPort))
	reply = append(reply, portBytes...)

	_, _ = c.Write(reply)
}

// do conn
func (s *TunnelModeServer) doConnect(c net.Conn, command uint8) {
	addrType := make([]byte, 1)
	_, _ = c.Read(addrType)
	var host string
	switch addrType[0] {
	case ipV4:
		ipv4 := make(net.IP, net.IPv4len)
		_, _ = c.Read(ipv4)
		host = ipv4.String()
	case ipV6:
		ipv6 := make(net.IP, net.IPv6len)
		_, _ = c.Read(ipv6)
		host = ipv6.String()
	case domainName:
		var domainLen uint8
		_ = binary.Read(c, binary.BigEndian, &domainLen)
		domain := make([]byte, domainLen)
		_, _ = c.Read(domain)
		host = string(domain)
	default:
		s.sendReply(c, addrTypeNotSupported)
		return
	}

	var port uint16
	_ = binary.Read(c, binary.BigEndian, &port)
	// connect to host
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	var ltype string
	if command == associateMethod {
		ltype = common.CONN_UDP
	} else {
		ltype = common.CONN_TCP
	}
	_ = s.DealClient(conn.NewConn(c), s.Task.Client, addr, nil, ltype, func() {
		s.sendReply(c, succeeded)
	}, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
	return
}

// conn
func (s *TunnelModeServer) handleConnect(c net.Conn) {
	s.doConnect(c, connectMethod)
}

// passive mode
func (s *TunnelModeServer) handleBind(c net.Conn) {
	s.sendReply(c, commandNotSupported)
	_ = c.Close()
}
func (s *TunnelModeServer) sendUdpReply(writeConn net.Conn, c net.Conn, rep uint8, serverIp string) {
	reply := []byte{
		5,
		rep,
		0,
		1,
	}
	localHost, localPort, _ := net.SplitHostPort(c.LocalAddr().String())
	localHost = serverIp
	ipBytes := net.ParseIP(localHost).To4()
	nPort, _ := strconv.Atoi(localPort)
	reply = append(reply, ipBytes...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(nPort))
	reply = append(reply, portBytes...)
	_, _ = writeConn.Write(reply)
}

func (s *TunnelModeServer) handleUDP(c net.Conn) {
	if tcpConn, ok := c.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(15 * time.Second)
		_ = transport.SetTcpKeepAliveParams(tcpConn, 15, 15, 3)
	}
	defer c.Close()
	addrType := make([]byte, 1)
	_, _ = io.ReadFull(c, addrType)
	var host string
	switch addrType[0] {
	case ipV4:
		ipv4 := make(net.IP, net.IPv4len)
		if _, err := io.ReadFull(c, ipv4); err != nil {
			s.sendReply(c, addrTypeNotSupported)
			return
		}
		host = ipv4.String()
	case ipV6:
		ipv6 := make(net.IP, net.IPv6len)
		if _, err := io.ReadFull(c, ipv6); err != nil {
			s.sendReply(c, addrTypeNotSupported)
			return
		}
		host = ipv6.String()
	case domainName:
		var domainLen uint8
		if err := binary.Read(c, binary.BigEndian, &domainLen); err != nil {
			s.sendReply(c, addrTypeNotSupported)
			return
		}
		domain := make([]byte, domainLen)
		if _, err := io.ReadFull(c, domain); err != nil {
			s.sendReply(c, addrTypeNotSupported)
			return
		}
		host = string(domain)
	default:
		s.sendReply(c, addrTypeNotSupported)
		return
	}
	// read port
	var port uint16
	if err := binary.Read(c, binary.BigEndian, &port); err != nil {
		s.sendReply(c, addrTypeNotSupported)
		return
	}
	logs.Trace("ASSOCIATE %s:%d", host, port)
	// get listen addr
	replyAddr, err := net.ResolveUDPAddr("udp", s.Task.ServerIp+":0")
	if err != nil {
		s.sendReply(c, addrTypeNotSupported)
		logs.Error("resolve local udp addr error: %v", err)
		return
	}
	reply, err := net.ListenUDP("udp", replyAddr)
	if err != nil {
		s.sendReply(c, addrTypeNotSupported)
		logs.Error("listen local reply udp port error: %v", err)
		return
	}
	defer reply.Close()
	// reply the local addr
	s.sendUdpReply(c, reply, succeeded, common.GetServerIpByClientIp(c.RemoteAddr().(*net.TCPAddr).IP))
	// new a tunnel to client
	link := conn.NewLink("udp5", "", s.Task.Client.Cnf.Crypt, s.Task.Client.Cnf.Compress, c.RemoteAddr().String(), s.AllowLocalProxy && s.Task.Target.LocalProxy)
	link.Option.Timeout = time.Second * 180
	target, err := s.Bridge.SendLinkInfo(s.Task.Client.Id, link, s.Task)
	if err != nil {
		logs.Warn("get connection from client Id %d error: %v", s.Task.Client.Id, err)
		return
	}
	defer target.Close()
	timeoutConn := conn.NewTimeoutConn(target, link.Option.Timeout)
	defer timeoutConn.Close()
	flowConn := conn.NewFlowConn(timeoutConn, s.Task.Flow, s.Task.Client.Flow)

	var clientIP net.IP
	var clientAddr atomic.Pointer[net.UDPAddr]

	// local UDP -> tunnel
	go func() {
		b := common.BufPoolUdp.Get().([]byte)
		defer common.BufPoolUdp.Put(b)

		for {
			n, lAddr, err := reply.ReadFromUDP(b)
			if err != nil {
				logs.Debug("read data from %v err %v", reply.LocalAddr(), err)
				_ = c.Close()
				_ = flowConn.Close()
				return
			}
			if clientIP == nil {
				clientIP = lAddr.IP
			}
			if !lAddr.IP.Equal(clientIP) {
				logs.Debug("ignore udp from unexpected ip: %v", lAddr.IP)
				continue
			}
			clientAddr.Store(lAddr)
			if _, err := flowConn.Write(b[:n]); err != nil {
				logs.Debug("write data to client error %v", err)
				_ = c.Close()
				_ = flowConn.Close()
				return
			}
		}
	}()

	// tunnel -> local UDP
	go func() {
		var l int32
		b := common.BufPoolUdp.Get().([]byte)
		defer common.BufPoolUdp.Put(b)

		for {
			if err := binary.Read(flowConn, binary.LittleEndian, &l); err != nil || l <= 0 || l >= common.PoolSizeUdp {
				logs.Debug("read len error %v", err)
				_ = c.Close()
				_ = flowConn.Close()
				return
			}
			if _, err := io.ReadFull(flowConn, b[:l]); err != nil {
				logs.Warn("read data form client error %v", err)
				_ = c.Close()
				_ = flowConn.Close()
				return
			}
			if addr := clientAddr.Load(); addr != nil {
				if _, err := reply.WriteTo(b[:l], addr); err != nil {
					logs.Warn("write data to user %v", err)
					_ = c.Close()
					_ = flowConn.Close()
					return
				}
			}
		}
	}()

	b := common.BufPoolUdp.Get().([]byte)
	defer common.BufPoolUdp.Put(b)
	for {
		if _, err := c.Read(b); err != nil {
			_ = flowConn.Close()
			return
		}
	}
}

func (s *TunnelModeServer) SocksAuth(c net.Conn) error {
	header := []byte{0, 0}
	if _, err := io.ReadAtLeast(c, header, 2); err != nil {
		return err
	}
	if header[0] != userAuthVersion {
		return errors.New("auth method not supported")
	}
	userLen := int(header[1])
	user := make([]byte, userLen)
	if _, err := io.ReadAtLeast(c, user, userLen); err != nil {
		return err
	}
	if _, err := c.Read(header[:1]); err != nil {
		return errors.New("failed to read password length")
	}
	passLen := int(header[0])
	pass := make([]byte, passLen)
	if _, err := io.ReadAtLeast(c, pass, passLen); err != nil {
		return err
	}

	if common.CheckAuthWithAccountMap(string(user), string(pass), s.Task.Client.Cnf.U, s.Task.Client.Cnf.P, file.GetAccountMap(s.Task.MultiAccount), file.GetAccountMap(s.Task.UserAuth)) {
		if _, err := c.Write([]byte{userAuthVersion, authSuccess}); err != nil {
			return err
		}
		return nil
	} else {
		if _, err := c.Write([]byte{userAuthVersion, authFailure}); err != nil {
			return err
		}
		return errors.New("auth failed")
	}
}

func ProcessMix(c *conn.Conn, s *TunnelModeServer) error {
	switch s.Task.Mode {
	case "socks5":
		s.Task.Mode = "mixProxy"
		s.Task.HttpProxy = false
		s.Task.Socks5Proxy = true
	case "httpProxy":
		s.Task.Mode = "mixProxy"
		s.Task.HttpProxy = true
		s.Task.Socks5Proxy = false
	}

	buf := make([]byte, 2)
	if _, err := io.ReadFull(c, buf); err != nil {
		logs.Warn("negotiation err %v", err)
		_ = c.Close()
		return err
	}

	if version := buf[0]; version != 5 {
		method := string(buf)
		switch method {
		case "GE", "PO", "HE", "PU ", "DE", "OP", "CO", "TR", "PA", "PR", "MK", "MO", "LO", "UN", "RE", "AC", "SE", "LI":
			if !s.Task.HttpProxy {
				logs.Warn("http proxy is disable, client %d request from: %v", s.Task.Client.Id, c.RemoteAddr())
				_ = c.Close()
				return errors.New("http proxy is disabled")
			}

			//ss := NewTunnelModeServer(ProcessHttp, s.Bridge, s.Task)
			//defer ss.Close()
			if err := ProcessHttp(c.SetRb(buf), s); err != nil {
				logs.Warn("http proxy error: %v", err)
				_ = c.Close()
				return err
			}
			_ = c.Close()
			return nil
		}
		logs.Trace("Socks5 Buf: %s", buf)
		logs.Warn("only support socks5 and http, request from: %v", c.RemoteAddr())
		_ = c.Close()
		return errors.New("unknown protocol")
	}

	if !s.Task.Socks5Proxy {
		logs.Warn("socks5 proxy is disable, client %d request from: %v", s.Task.Client.Id, c.RemoteAddr())
		_ = c.Close()
		return errors.New("socks5 proxy is disabled")
	}

	nMethods := buf[1]
	methods := make([]byte, nMethods)
	if l, err := c.Read(methods); l != int(nMethods) || err != nil {
		logs.Warn("wrong method")
		_ = c.Close()
		return errors.New("wrong method")
	}
	if (s.Task.Client.Cnf.U != "" && s.Task.Client.Cnf.P != "") || (s.Task.MultiAccount != nil && len(s.Task.MultiAccount.AccountMap) > 0) || (s.Task.UserAuth != nil && len(s.Task.UserAuth.AccountMap) > 0) {
		buf[1] = UserPassAuth
		_, _ = c.Write(buf)
		if err := s.SocksAuth(c); err != nil {
			_ = c.Close()
			logs.Warn("Validation failed: %v", err)
			return err
		}
	} else {
		buf[1] = 0
		_, _ = c.Write(buf)
	}
	s.handleSocks5Request(c)
	return nil
}
