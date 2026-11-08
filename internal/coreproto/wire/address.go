package wire

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"

	"golang.org/x/net/idna"
)

// Address types (§1).
const (
	atypIPv4   = 1
	atypDomain = 3
	atypIPv6   = 4
)

// Address is a destination or a source: an IP, or a domain (A-label) when
// Host is set.
type Address struct {
	IP   netip.Addr
	Host string
	Port uint16
}

// String is host:port.
func (a Address) String() string {
	h := a.Host
	if h == "" {
		return netip.AddrPortFrom(a.IP.Unmap(), a.Port).String()
	}
	return h + ":" + strconv.Itoa(int(a.Port))
}

// AppendAddress appends a; an IPv4-mapped IPv6 address is written as IPv4.
func AppendAddress(b []byte, a Address) ([]byte, error) {
	switch {
	case a.Host != "":
		if len(a.Host) > 255 {
			return b, ErrMalformed
		}
		b = append(b, atypDomain)
		b = append(b, a.Host...)
	case a.IP.Unmap().Is4():
		v := a.IP.Unmap().As4()
		b = append(append(b, atypIPv4), v[:]...)
	case a.IP.Is6():
		v := a.IP.As16()
		b = append(append(b, atypIPv6), v[:]...)
	default:
		return b, ErrMalformed
	}
	return binary.BigEndian.AppendUint16(b, a.Port), nil
}

// ParseAddress parses all of b. Port 0 is left to the caller: it is invalid
// for a target and "unknown" for a source.
func ParseAddress(b []byte) (Address, error) {
	if len(b) < 3 {
		return Address{}, ErrMalformed
	}
	var a Address
	body, port := b[1:len(b)-2], b[len(b)-2:]
	a.Port = binary.BigEndian.Uint16(port)
	switch b[0] {
	case atypIPv4:
		if len(body) != 4 {
			return Address{}, ErrMalformed
		}
		a.IP = netip.AddrFrom4([4]byte(body))
	case atypIPv6:
		if len(body) != 16 {
			return Address{}, ErrMalformed
		}
		a.IP = netip.AddrFrom16([16]byte(body)).Unmap()
	case atypDomain:
		if len(body) < 1 || len(body) > 255 {
			return Address{}, ErrMalformed
		}
		a.Host = string(body)
	default:
		return Address{}, ErrMalformed
	}
	return a, nil
}

// ParseHostPort reads the host:port of a target or a source: an IP, or a name
// written as its A-label.
func ParseHostPort(s string) (Address, error) {
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return Address{}, ErrMalformed
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return Address{}, ErrMalformed
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return Address{IP: ip.WithZone("").Unmap(), Port: uint16(port)}, nil
	}
	if h, err = idna.Lookup.ToASCII(h); err != nil || h == "" || len(h) > 255 {
		return Address{}, ErrMalformed
	}
	return Address{Host: h, Port: uint16(port)}, nil
}
