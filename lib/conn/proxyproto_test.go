package conn

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
)

const proxyV2Sig = "\r\n\r\n\x00\r\nQUIT\n"

func proxyV2(famProto byte, src, dst netip.AddrPort) []byte {
	b := append([]byte(proxyV2Sig), 0x21, famProto)
	ips := append(src.Addr().AsSlice(), dst.Addr().AsSlice()...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(ips)+4))
	b = append(b, ips...)
	b = binary.BigEndian.AppendUint16(b, src.Port())
	return binary.BigEndian.AppendUint16(b, dst.Port())
}

func TestProxyProtocolHeader(t *testing.T) {
	tcp := func(s string) *net.TCPAddr { return net.TCPAddrFromAddrPort(netip.MustParseAddrPort(s)) }
	udp := func(s string) *net.UDPAddr { return net.UDPAddrFromAddrPort(netip.MustParseAddrPort(s)) }
	ap := netip.MustParseAddrPort

	for _, tc := range []struct {
		name     string
		version  int
		src, dst net.Addr
		want     []byte
	}{
		{"v1 tcp4", 1, tcp("192.0.2.1:1234"), tcp("198.51.100.2:80"),
			[]byte("PROXY TCP4 192.0.2.1 198.51.100.2 1234 80\r\n")},
		{"v1 tcp6", 1, tcp("[2001:db8::1]:1234"), tcp("[2001:db8::2]:80"),
			[]byte("PROXY TCP6 2001:db8::1 2001:db8::2 1234 80\r\n")},
		{"v1 ipv4-mapped source", 1, tcp("[::ffff:192.0.2.1]:1234"), tcp("198.51.100.2:80"),
			[]byte("PROXY TCP4 192.0.2.1 198.51.100.2 1234 80\r\n")},
		{"v1 ipv6 source ipv4 target", 1, tcp("[2001:db8::1]:1234"), tcp("198.51.100.2:80"),
			[]byte("PROXY TCP6 2001:db8::1 ::ffff:198.51.100.2 1234 80\r\n")},
		{"v1 ipv4 source ipv6 target", 1, tcp("192.0.2.1:1234"), tcp("[2001:db8::2]:80"),
			[]byte("PROXY TCP6 ::ffff:192.0.2.1 2001:db8::2 1234 80\r\n")},
		{"v1 udp uses tcp token", 1, udp("192.0.2.1:1234"), udp("198.51.100.2:53"),
			[]byte("PROXY TCP4 192.0.2.1 198.51.100.2 1234 53\r\n")},
		{"v1 unspecified target keeps port", 1, udp("[2001:db8::1]:1234"), &net.UDPAddr{Port: 9000},
			[]byte("PROXY TCP6 2001:db8::1 :: 1234 9000\r\n")},
		{"v1 nil target", 1, tcp("192.0.2.1:1234"), (*net.TCPAddr)(nil),
			[]byte("PROXY TCP4 192.0.2.1 0.0.0.0 1234 0\r\n")},
		{"v1 nil source", 1, nil, tcp("198.51.100.2:80"), []byte("PROXY UNKNOWN\r\n")},
		{"v1 mismatched types", 1, tcp("192.0.2.1:1234"), udp("198.51.100.2:80"), []byte("PROXY UNKNOWN\r\n")},
		{"v2 tcp4", 2, tcp("192.0.2.1:1234"), tcp("198.51.100.2:80"),
			proxyV2(0x11, ap("192.0.2.1:1234"), ap("198.51.100.2:80"))},
		{"v2 udp6", 2, udp("[2001:db8::1]:1234"), udp("[2001:db8::2]:53"),
			proxyV2(0x22, ap("[2001:db8::1]:1234"), ap("[2001:db8::2]:53"))},
		{"v2 mixed families", 2, tcp("192.0.2.1:1234"), tcp("[2001:db8::2]:80"),
			proxyV2(0x21, ap("[::ffff:192.0.2.1]:1234"), ap("[2001:db8::2]:80"))},
		{"v2 unspecified udp target", 2, udp("192.0.2.1:1234"), &net.UDPAddr{Port: 9000},
			proxyV2(0x12, ap("192.0.2.1:1234"), ap("0.0.0.0:9000"))},
		{"v2 nil source", 2, nil, tcp("198.51.100.2:80"),
			append([]byte(proxyV2Sig), 0x21, 0x00, 0, 0)},
		{"v2 mismatched types", 2, udp("192.0.2.1:1234"), tcp("198.51.100.2:80"),
			append([]byte(proxyV2Sig), 0x21, 0x00, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := ProxyProtocolHeader(tc.version, tc.src, tc.dst)
			if h == nil {
				t.Fatal("no header")
			}
			var buf bytes.Buffer
			if _, err := h.WriteTo(&buf); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), tc.want) {
				t.Fatalf("header = %q, want %q", buf.Bytes(), tc.want)
			}
		})
	}
}

func TestProxyProtocolHeaderOff(t *testing.T) {
	src := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 1}
	for _, v := range []int{0, 3, -1} {
		if h := ProxyProtocolHeader(v, src, src); h != nil {
			t.Fatalf("version %d: header %+v, want none", v, h)
		}
	}
}

func TestProxyProtocolHeaderKeepsTarget(t *testing.T) {
	dst := &net.TCPAddr{IP: net.IPv4(198, 51, 100, 2), Port: 80}
	ProxyProtocolHeader(1, &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 1}, dst)
	if !dst.IP.Equal(net.IPv4(198, 51, 100, 2)) || len(net.IPv6zero) != net.IPv6len || !net.IPv6zero.IsUnspecified() {
		t.Fatalf("addresses changed: target %v, IPv6zero %v", dst, net.IPv6zero)
	}
}
