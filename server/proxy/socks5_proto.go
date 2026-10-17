package proxy

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
)

var (
	errSocks5InvalidRequest      = errors.New("socks5: invalid request")
	errSocks5UnsupportedAddrType = errors.New("socks5: unsupported address type")
	errSocks5UnsupportedAuthVer  = errors.New("socks5: unsupported auth version")
)

type socks5Address struct {
	Type byte
	Host string
	Port uint16
}

func (a socks5Address) String() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(int(a.Port)))
}

type socks5Request struct {
	Command     byte
	Destination socks5Address
}

func readSocks5Request(r io.Reader) (socks5Request, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return socks5Request{}, err
	}
	if header[0] != 5 || header[2] != 0 {
		return socks5Request{}, errSocks5InvalidRequest
	}
	addr, err := readSocks5Address(r, header[3])
	if err != nil {
		return socks5Request{}, err
	}
	return socks5Request{
		Command:     header[1],
		Destination: addr,
	}, nil
}

func readSocks5Address(r io.Reader, atyp byte) (socks5Address, error) {
	host, err := readSocks5Host(r, atyp)
	if err != nil {
		return socks5Address{}, err
	}
	var port uint16
	if err := binary.Read(r, binary.BigEndian, &port); err != nil {
		return socks5Address{}, err
	}
	return socks5Address{Type: atyp, Host: host, Port: port}, nil
}

func readSocks5Host(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case ipV4:
		buf := make(net.IP, net.IPv4len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	case ipV6:
		buf := make(net.IP, net.IPv6len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	case domainName:
		return readSocks5Domain(r)
	default:
		return "", errSocks5UnsupportedAddrType
	}
}

func readSocks5Domain(r io.Reader) (string, error) {
	var length [1]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return "", err
	}
	if length[0] == 0 {
		return "", errSocks5InvalidRequest
	}
	buf := make([]byte, int(length[0]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readSocks5Methods(r io.Reader, n int) ([]byte, error) {
	if n < 0 {
		return nil, errSocks5InvalidRequest
	}
	methods := make([]byte, n)
	_, err := io.ReadFull(r, methods)
	return methods, err
}

func readSocks5UserPassCredentials(r io.Reader) (string, string, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", "", err
	}
	if header[0] != userAuthVersion {
		return "", "", errSocks5UnsupportedAuthVer
	}
	usernameLen := int(header[1])
	username := make([]byte, usernameLen)
	if _, err := io.ReadFull(r, username); err != nil {
		return "", "", err
	}
	if _, err := io.ReadFull(r, header[:1]); err != nil {
		return "", "", err
	}
	passwordLen := int(header[0])
	password := make([]byte, passwordLen)
	if _, err := io.ReadFull(r, password); err != nil {
		return "", "", err
	}
	return string(username), string(password), nil
}

func socks5ConnReplyAddr(c net.Conn) socks5Address {
	if c == nil {
		return socks5ZeroReplyAddr(false, 0)
	}
	return socks5ReplyAddrFromNetAddr(nil, c.LocalAddr(), nil, 0)
}

func socks5ConnectReplyAddr(task *file.Tunnel, clientConn net.Conn) socks5Address {
	return socks5ReplyAddrFromNetAddr(task, addrOf(clientConn), nil, taskPort(task))
}

func socks5UDPReplyAddr(task *file.Tunnel, tcpConn net.Conn, udpConn net.PacketConn, clientIP net.IP) socks5Address {
	var tcpLocal net.Addr
	if tcpConn != nil {
		tcpLocal = tcpConn.LocalAddr()
	}
	var udpLocal net.Addr
	if udpConn != nil {
		udpLocal = udpConn.LocalAddr()
	}
	fallbackPort := 0
	if task != nil {
		fallbackPort = task.Port
	}
	return socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, fallbackPort, clientIP)
}

func socks5ReplyAddrFromNetAddr(task *file.Tunnel, tcpLocal, udpLocal net.Addr, fallbackPort int, clientIP ...net.IP) socks5Address {
	var client net.IP
	if len(clientIP) > 0 {
		client = common.NormalizeIP(clientIP[0])
	}
	wantV6 := client != nil && client.To4() == nil

	ip := socks5ReplyIP(task, tcpLocal, udpLocal, client, wantV6)
	port := socks5ReplyPort(udpLocal, tcpLocal, fallbackPort)
	if port <= 0 {
		port = fallbackPort
	}
	if ip == nil {
		return socks5ZeroReplyAddr(wantV6, port)
	}
	if v4 := ip.To4(); v4 != nil {
		return socks5Address{Type: ipV4, Host: v4.String(), Port: uint16(port)}
	}
	return socks5Address{Type: ipV6, Host: ip.To16().String(), Port: uint16(port)}
}

func addrOf(c net.Conn) net.Addr {
	if c == nil {
		return nil
	}
	return c.LocalAddr()
}

func taskPort(task *file.Tunnel) int {
	if task == nil {
		return 0
	}
	return task.Port
}

// socks5ReplyIP picks the address to advertise in a reply. An explicitly
// configured ServerIp always wins. A local address is only advertised when the
// client can reach it: a private local address is withheld from a public client
// (for example behind 1:1 NAT), and the zero address tells the client to reuse
// the server address of its control connection instead.
func socks5ReplyIP(task *file.Tunnel, tcpLocal, udpLocal net.Addr, client net.IP, wantV6 bool) net.IP {
	publicClient := client != nil && common.IsPublicIP(client)
	trySpecificIP := func(value net.IP) net.IP {
		value = common.NormalizeIP(value)
		if value == nil || value.IsUnspecified() || common.IsZeroIP(value) {
			return nil
		}
		if (value.To4() == nil) != wantV6 {
			return nil
		}
		return value
	}

	if task != nil {
		if ip := net.ParseIP(task.ServerIp); ip != nil {
			if specific := trySpecificIP(ip); specific != nil {
				return specific
			}
		}
	}
	for _, addr := range []net.Addr{udpLocal, tcpLocal} {
		var local net.IP
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if typed != nil {
				local = typed.IP
			}
		case *net.TCPAddr:
			if typed != nil {
				local = typed.IP
			}
		}
		specific := trySpecificIP(local)
		if specific == nil {
			continue
		}
		if publicClient && !common.IsPublicIP(specific) {
			continue
		}
		return specific
	}
	return nil
}

func socks5ReplyPort(primary, secondary net.Addr, fallback int) int {
	for _, addr := range []net.Addr{primary, secondary} {
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		case *net.TCPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		}
	}
	return fallback
}

func socks5ZeroReplyAddr(wantV6 bool, port int) socks5Address {
	if wantV6 {
		return socks5Address{Type: ipV6, Host: net.IPv6zero.String(), Port: uint16(port)}
	}
	return socks5Address{Type: ipV4, Host: net.IPv4zero.String(), Port: uint16(port)}
}

func writeSocks5Reply(w io.Writer, rep uint8, addr socks5Address) error {
	reply := make([]byte, 0, 22)
	reply = append(reply, 5, rep, 0, addr.Type)
	switch addr.Type {
	case ipV4:
		ip := net.ParseIP(addr.Host).To4()
		if ip == nil {
			ip = net.IPv4zero.To4()
		}
		reply = append(reply, ip...)
	case ipV6:
		ip := net.ParseIP(addr.Host).To16()
		if ip == nil {
			ip = net.IPv6zero.To16()
		}
		reply = append(reply, ip...)
	case domainName:
		host := addr.Host
		if len(host) > 255 {
			host = host[:255]
		}
		reply = append(reply, byte(len(host)))
		reply = append(reply, host...)
	default:
		reply = append(reply, net.IPv4zero.To4()...)
	}
	reply = binary.BigEndian.AppendUint16(reply, addr.Port)
	_, err := w.Write(reply)
	return err
}
