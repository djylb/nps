package common

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

type countingWriter struct {
	writes int
	buf    bytes.Buffer
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.buf.Write(p)
}

func TestUDPDatagramWriteMatchesSocksFormat(t *testing.T) {
	d := &UDPDatagram{
		Header: &UDPHeader{
			Rsv:  0,
			Frag: 0,
			Addr: &Addr{Type: domainName, Host: "example.com", Port: 8080},
		},
		Data: []byte("payload"),
	}

	var out bytes.Buffer
	if err := d.Write(&out); err != nil {
		t.Fatalf("UDPDatagram.Write() error = %v", err)
	}

	got := out.Bytes()
	if len(got) < 7 {
		t.Fatalf("UDPDatagram.Write() produced short packet: %d", len(got))
	}

	if rsv := binary.BigEndian.Uint16(got[:2]); rsv != 0 {
		t.Fatalf("RSV = %d, want 0", rsv)
	}
	if got[2] != 0 {
		t.Fatalf("FRAG = %d, want 0", got[2])
	}
	if got[3] != domainName {
		t.Fatalf("ATYP = %d, want %d", got[3], domainName)
	}
	if got[4] != byte(len("example.com")) {
		t.Fatalf("domain length = %d, want %d", got[4], len("example.com"))
	}

	offset := 5
	hostEnd := offset + len("example.com")
	if host := string(got[offset:hostEnd]); host != "example.com" {
		t.Fatalf("host = %q, want %q", host, "example.com")
	}
	if port := binary.BigEndian.Uint16(got[hostEnd : hostEnd+2]); port != 8080 {
		t.Fatalf("port = %d, want %d", port, 8080)
	}
	if payload := string(got[hostEnd+2:]); payload != "payload" {
		t.Fatalf("payload = %q, want %q", payload, "payload")
	}
}

func TestUDPDatagramWriteSupportsNilHeader(t *testing.T) {
	d := &UDPDatagram{Data: []byte("x")}

	var out bytes.Buffer
	if err := d.Write(&out); err != nil {
		t.Fatalf("UDPDatagram.Write() error with nil header = %v", err)
	}

	got := out.Bytes()
	if len(got) != 11 { // RSV(2)+FRAG(1)+ATYP(1)+IPv4(4)+PORT(2)+payload(1)
		t.Fatalf("packet length = %d, want 11", len(got))
	}
	if got[3] != ipV4 {
		t.Fatalf("ATYP = %d, want %d", got[3], ipV4)
	}
	if !net.IP(got[4:8]).Equal(net.IPv4zero) {
		t.Fatalf("ip = %v, want %v", net.IP(got[4:8]), net.IPv4zero)
	}
}

func TestUDPDatagramWriteUsesSingleWriteCall(t *testing.T) {
	d := &UDPDatagram{
		Header: &UDPHeader{Addr: &Addr{Type: ipV4, Host: "127.0.0.1", Port: 80}},
		Data:   []byte("abc"),
	}

	w := &countingWriter{}
	if err := d.Write(w); err != nil {
		t.Fatalf("UDPDatagram.Write() error = %v", err)
	}
	if w.writes != 1 {
		t.Fatalf("write calls = %d, want 1", w.writes)
	}
}
