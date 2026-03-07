package common

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDomainCheck(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{name: "plain domain", input: "example.com", valid: true},
		{name: "http domain", input: "http://example.com", valid: true},
		{name: "https domain with path", input: "https://example.com/path", valid: true},
		{name: "invalid ip", input: "127.0.0.1", valid: false},
		{name: "invalid string", input: "not_a_domain", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DomainCheck(tc.input); got != tc.valid {
				t.Fatalf("DomainCheck(%q) = %v, want %v", tc.input, got, tc.valid)
			}
		})
	}
}

func TestGetWriteStr(t *testing.T) {
	got := GetWriteStr("alpha", "beta")
	want := []byte("alpha" + CONN_DATA_SEQ + "beta" + CONN_DATA_SEQ)
	if !bytes.Equal(got, want) {
		t.Fatalf("GetWriteStr() = %q, want %q", string(got), string(want))
	}
}

func TestBinaryWrite(t *testing.T) {
	raw := bytes.NewBuffer(nil)
	BinaryWrite(raw, "info", "true")

	buf := raw.Bytes()
	if len(buf) < 4 {
		t.Fatalf("BinaryWrite() output too short: %d", len(buf))
	}

	payloadLen := int(binary.LittleEndian.Uint32(buf[:4]))
	payload := buf[4:]
	if payloadLen != len(payload) {
		t.Fatalf("payload length = %d, want %d", payloadLen, len(payload))
	}

	want := []byte("info" + CONN_DATA_SEQ + "true" + CONN_DATA_SEQ)
	if !bytes.Equal(payload, want) {
		t.Fatalf("payload = %q, want %q", string(payload), string(want))
	}
}
