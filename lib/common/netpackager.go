package common

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
)

type NetPackager interface {
	Pack(writer io.Writer) (err error)
	UnPack(reader io.Reader) (err error)
}

const (
	ipV4       = 1
	domainName = 3
	ipV6       = 4
)

type UDPHeader struct {
	Rsv  uint16
	Frag uint8
	Addr *Addr
}

func NewUDPHeader(rsv uint16, frag uint8, addr *Addr) *UDPHeader {
	return &UDPHeader{
		Rsv:  rsv,
		Frag: frag,
		Addr: addr,
	}
}

type Addr struct {
	Type uint8
	Host string
	Port uint16
}

func (addr *Addr) String() string {
	return net.JoinHostPort(addr.Host, strconv.Itoa(int(addr.Port)))
}

func (addr *Addr) Decode(b []byte) error {
	if len(b) < 1 {
		return errors.New("decode error: short buffer")
	}
	addr.Type = b[0]
	pos := 1
	switch addr.Type {
	case ipV4:
		if len(b) < pos+net.IPv4len+2 {
			return errors.New("decode error: short buffer for ipv4")
		}
		addr.Host = net.IP(b[pos : pos+net.IPv4len]).String()
		pos += net.IPv4len
	case ipV6:
		if len(b) < pos+net.IPv6len+2 {
			return errors.New("decode error: short buffer for ipv6")
		}
		addr.Host = net.IP(b[pos : pos+net.IPv6len]).String()
		pos += net.IPv6len
	case domainName:
		if len(b) < pos+1 {
			return errors.New("decode error: short buffer for domain length")
		}
		addrlen := int(b[pos])
		pos++
		if len(b) < pos+addrlen+2 {
			return errors.New("decode error: short buffer for domain data")
		}
		addr.Host = string(b[pos : pos+addrlen])
		pos += addrlen
	default:
		return errors.New("decode error: unsupported atyp")
	}

	addr.Port = binary.BigEndian.Uint16(b[pos:])

	return nil
}

func (addr *Addr) Encode(b []byte) (int, error) {
	b[0] = addr.Type
	pos := 1
	switch addr.Type {
	case ipV4:
		ip4 := net.ParseIP(addr.Host).To4()
		if ip4 == nil {
			ip4 = net.IPv4zero.To4()
		}
		pos += copy(b[pos:], ip4)
	case domainName:
		h := addr.Host
		if len(h) > 255 { // SOCKS5 domain length is 1 byte
			h = h[:255]
		}
		b[pos] = byte(len(h))
		pos++
		pos += copy(b[pos:], h)
	case ipV6:
		ip16 := net.ParseIP(addr.Host).To16()
		if ip16 == nil {
			ip16 = net.IPv6zero.To16()
		}
		pos += copy(b[pos:], ip16)
	default:
		// fallback to IPv4 zero
		b[0] = ipV4
		copy(b[pos:pos+4], net.IPv4zero.To4())
		pos += 4
	}
	binary.BigEndian.PutUint16(b[pos:], addr.Port)
	pos += 2

	return pos, nil
}

func (h *UDPHeader) Write(w io.Writer) error {
	b := BufPoolUdp.Get().([]byte)
	defer BufPoolUdp.Put(b)

	binary.BigEndian.PutUint16(b[:2], h.Rsv)
	b[2] = h.Frag

	addr := h.Addr
	if addr == nil {
		addr = &Addr{}
	}
	length, _ := addr.Encode(b[3:])

	_, err := w.Write(b[:3+length])
	return err
}

type UDPDatagram struct {
	Header *UDPHeader
	Data   []byte
}

func ReadUDPDatagram(r io.Reader) (*UDPDatagram, error) {
	b := BufPoolUdp.Get().([]byte)
	defer BufPoolUdp.Put(b)

	// ensure we have RSV(2) + FRAG(1) + ATYP(1) + at least 1 more byte (for domain length)
	n, err := io.ReadFull(r, b[:5])
	if err != nil {
		return nil, err
	}

	header := &UDPHeader{
		Rsv:  binary.BigEndian.Uint16(b[:2]),
		Frag: b[2],
	}

	// SOCKS5 only supports FRAG == 0
	if header.Frag != 0 {
		return nil, errors.New("socks5 udp: fragment not supported")
	}

	atype := b[3]
	hlen := 0
	switch atype {
	case ipV4:
		hlen = 10 // 2+1+1 + 4 + 2
	case ipV6:
		hlen = 22 // 2+1+1 + 16 + 2
	case domainName:
		hlen = 7 + int(b[4]) // 2+1+1 + 1 + len + 2
	default:
		return nil, errors.New("addr not support")
	}

	dlen := int(header.Rsv)
	if dlen == 0 {
		// standard SOCKS5 UDP datagram: read the rest (we assume r is bounded, e.g., framed)
		extra, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		copy(b[n:], extra)
		n += len(extra) // total length
		dlen = n - hlen // payload length
	} else {
		// extended feature: RSV carries data length
		if _, err := io.ReadFull(r, b[n:hlen+dlen]); err != nil {
			return nil, err
		}
		n = hlen + dlen
	}

	header.Addr = new(Addr)
	if err := header.Addr.Decode(b[3:hlen]); err != nil {
		return nil, err
	}
	data := make([]byte, dlen)
	copy(data, b[hlen:n])
	d := &UDPDatagram{
		Header: header,
		Data:   data,
	}
	return d, nil
}

func NewUDPDatagram(header *UDPHeader, data []byte) *UDPDatagram {
	return &UDPDatagram{
		Header: header,
		Data:   data,
	}
}

func (d *UDPDatagram) Write(w io.Writer) error {
	h := d.Header
	if h == nil {
		h = &UDPHeader{}
	}

	b := BufPoolUdp.Get().([]byte)
	defer BufPoolUdp.Put(b)

	binary.BigEndian.PutUint16(b[:2], h.Rsv)
	b[2] = h.Frag

	addr := h.Addr
	if addr == nil {
		addr = &Addr{}
	}
	headerLen, _ := addr.Encode(b[3:])
	headerLen += 3
	total := headerLen + len(d.Data)

	var packet []byte
	if total <= len(b) {
		packet = b[:total]
	} else {
		packet = make([]byte, total)
		copy(packet[:headerLen], b[:headerLen])
	}

	copy(packet[headerLen:], d.Data)

	for len(packet) > 0 {
		n, err := w.Write(packet)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}

// trim IPv6 zone if any (e.g., "fe80::1%eth0" -> "fe80::1")
func trimZone(host string) string {
	if i := strings.IndexByte(host, '%'); i >= 0 {
		return host[:i]
	}
	return host
}

func ToSocksAddr(addr net.Addr) *Addr {
	host := "0.0.0.0"
	port := 0
	typ := ipV4

	if addr != nil {
		h, p, _ := net.SplitHostPort(addr.String())
		host = trimZone(h)

		if ip := net.ParseIP(host); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				typ = ipV4
				host = v4.String()
			} else {
				typ = ipV6
				host = ip.String()
			}
		} else {
			typ = domainName
		}
		port, _ = strconv.Atoi(p)
	}

	return &Addr{
		Type: uint8(typ),
		Host: host,
		Port: uint16(port),
	}
}
