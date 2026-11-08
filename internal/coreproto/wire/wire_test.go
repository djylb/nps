package wire

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 63, 64, 16383, 16384, 1<<30 - 1, 1 << 30, MaxVarint} {
		b := AppendVarint(nil, v)
		got, n, err := ReadVarint(b)
		if err != nil || got != v || n != len(b) {
			t.Fatalf("%d: %d %d %v", v, got, n, err)
		}
	}
	// a non-shortest encoding is accepted
	if v, _, err := ReadVarint([]byte{0x40, 0x05}); err != nil || v != 5 {
		t.Fatal(v, err)
	}
}

func TestDecodeRules(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"id 0", AppendUint(nil, key(0, false, false), 1), ErrMalformed},
		{"wrong W", AppendBytes(nil, key(KeySeq.ID(), true, true), []byte{1}), ErrMalformed},
		{"wrong C", AppendUint(nil, key(KeySeq.ID(), false, false), 1), ErrMalformed},
		{"repeat", AppendUint(AppendUint(nil, KeySeq, 1), KeySeq, 2), ErrMalformed},
		{"unknown C=1", AppendUint(nil, key(40, true, false), 1), ErrUnsupported},
		{"unknown C=0", AppendUint(nil, key(40, false, false), 1), nil},
		{"grease C=1", AppendUint(nil, key(47, true, false), 1), nil},
		{"short", []byte{byte(KeyLabel), 5, 1}, ErrMalformed},
		{"repeated allowed", AppendUint(AppendUint(nil, KeyCap, 1), KeyCap, 2), nil},
	}
	for _, c := range cases {
		err := Common.Decode(c.b, func(Field) error { return nil })
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestHelloRoundTripAndC1(t *testing.T) {
	in := Hello{Caps: []uint64{1, 2}, CompressAlgs: []uint64{CompressFlate}, Label: "box", Agent: "npc"}
	got, err := ParseHello(AppendHello(nil, &in))
	if err != nil || got.Agent != "npc" || got.Label != "box" || !got.HasCap(2) || len(got.CompressAlgs) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ParseHello(AppendUint(nil, KeySeq, 1)); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
	if _, err := ParseHello(AppendUint(nil, key(40, true, false), 1)); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
}

func TestReplyHead(t *testing.T) {
	if b := AppendReply(nil, CodeOK, "", nil); !bytes.Equal(b, []byte{0}) {
		t.Fatalf("%x", b)
	}
	b := AppendReply(nil, CodeNotAllowed, "no", nil)
	r, err := ReadReply(bytes.NewReader(b), 1024)
	if err != nil || r.Code != CodeNotAllowed || r.Reason != "no" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAddress(t *testing.T) {
	mapped := Address{IP: netip.MustParseAddr("::ffff:1.2.3.4"), Port: 80}
	b, err := AppendAddress(nil, mapped)
	if err != nil || b[0] != atypIPv4 || len(b) != 7 {
		t.Fatalf("%x %v", b, err)
	}
	for _, a := range []Address{{Host: "example.com", Port: 443}, {IP: netip.MustParseAddr("2001:db8::1"), Port: 1}} {
		b, err := AppendAddress(nil, a)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseAddress(b)
		if err != nil || got != a {
			t.Fatalf("%v %v", got, err)
		}
	}
	if _, err := ParseAddress([]byte{9, 0, 0}); err == nil {
		t.Fatal("bad atyp accepted")
	}
}

func TestOpenInfoAndConfigOpen(t *testing.T) {
	b := AppendConfigOpen(7)
	op, f, err := ParseOpenOp(b)
	if err != nil || op != OpConfig {
		t.Fatal(op, err)
	}
	if seq, err := ParseConfigOpen(f); err != nil || seq != 7 {
		t.Fatal(seq, err)
	}
	if _, err := ParseConfigOpen(nil); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
}

func TestSnapshotUnknownRecordIsDefault(t *testing.T) {
	s, err := ParseSnapshot(AppendUint(nil, keyCfgRecord, 99))
	if err != nil || s.Record != RecordDefault {
		t.Fatal(s, err)
	}
	s, err = ParseSnapshot(AppendSnapshot(nil, &Snapshot{Record: RecordTrusted}))
	if err != nil || s.Record != RecordTrusted {
		t.Fatal(s, err)
	}
	if s, err := ParseSnapshot(nil); err != nil || s.Record != RecordDefault {
		t.Fatal(s, err)
	}
}

func TestDeriveAnyShape(t *testing.T) {
	for _, vkey := range []string{"abc", "x", "a-very-long-canonical-looking-vkey-0123456789"} {
		h, psk, err := Derive(vkey)
		if err != nil || len(h) != HandleLen || len(psk) != 32 {
			t.Fatalf("%q: %d %d %v", vkey, len(h), len(psk), err)
		}
		h2, psk2, _ := Derive(vkey)
		if !bytes.Equal(h, h2) || !bytes.Equal(psk, psk2) {
			t.Fatal("not deterministic")
		}
	}
}

func TestServiceRoundTrip(t *testing.T) {
	in := []Service{{ID: ServiceID("file", "a"), Kind: "file", Name: "a", Meta: "m"}, {ID: ServiceID("x", ""), Kind: "x"}}
	got, err := ParseServices(AppendServices(nil, in))
	if err != nil || len(got) != 2 || got[0].Name != "a" || got[0].Meta != "m" || got[1].Kind != "x" {
		t.Fatalf("%+v, %v", got, err)
	}
	if len(ServiceID("file", "a")) != ServiceIDLen {
		t.Fatal("id length")
	}
	// kind and name are length-prefixed: moving the boundary changes the id
	if string(ServiceID("ab", "c")) == string(ServiceID("a", "bc")) {
		t.Fatal("ambiguous id")
	}
	bad := []Service{{ID: ServiceID("file", "b"), Kind: "file", Name: "a"}}
	if _, err := ParseServices(AppendServices(nil, bad)); err != ErrMalformed {
		t.Fatalf("wrong id: %v", err)
	}
	seq, err := ParseServiceOpen(AppendServiceOpen(7)[1:])
	if err != nil || seq != 7 {
		t.Fatalf("seq %d, %v", seq, err)
	}
}

func TestHealthRoundTrip(t *testing.T) {
	in := Snapshot{Health: []HealthGroup{{Name: "t1", Type: "http", Path: "/p", Interval: 3, Timeout: 2, Fall: 1, Targets: []string{"a:1", "b:2"}}}}
	got, err := ParseSnapshot(AppendSnapshot(nil, &in))
	if err != nil || len(got.Health) != 1 || got.Health[0].Type != "http" || len(got.Health[0].Targets) != 2 || got.Health[0].Fall != 1 {
		t.Fatalf("%+v, %v", got.Health, err)
	}
	rec := AppendHealthReport(nil, HealthReport{Name: "t1", Addr: "a:1", Up: false})
	r, err := ParseHealthReport(rec[1:])
	if err != nil || r.Name != "t1" || r.Addr != "a:1" || r.Up {
		t.Fatalf("%+v, %v", r, err)
	}
}
