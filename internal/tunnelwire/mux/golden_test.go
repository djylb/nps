package mux

// testdata/frames.json is the mux wire format released clients depend on,
// recorded from the code they were built from. Do not update it to match a
// change.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

type frameVector struct {
	Name string
	Hex  string
}

func TestMuxGoldenFrames(t *testing.T) {
	var out []frameVector
	add := func(flag uint8, id int32, content any) {
		pack := muxPack.Get()
		if err := pack.Set(flag, id, content); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := pack.Pack(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, frameVector{fmt.Sprintf("flag%d/id%d", flag, id), hex.EncodeToString(b.Bytes())})
		// The frame must decode to what was packed.
		back := muxPack.Get()
		if _, err := back.UnPack(bytes.NewReader(b.Bytes())); err != nil {
			t.Fatal(err)
		}
		if back.flag != flag || back.id != id {
			t.Fatalf("decoded flag %d id %d, want %d %d", back.flag, back.id, flag, id)
		}
		back.reset()
	}
	add(muxPingFlag, muxPing, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	add(muxPingReturn, muxPing, []byte{8, 7, 6, 5, 4, 3, 2, 1})
	add(muxNewConnOk, 1, nil)
	add(muxNewConnFail, 2, nil)
	add(muxNewMsg, 3, []byte("hello"))
	add(muxNewMsgPart, -3, []byte("part"))
	add(muxMsgSendOk, 4, uint64(0x0102030405060708))
	add(muxNewConn, 5, nil)
	add(muxConnClose, 6, nil)

	// A session: the frames of a stream from open to close, without the pings
	// and window updates that depend on timing.
	a, z := net.Pipe()
	defer func() { _ = a.Close(); _ = z.Close() }()
	frames := make(chan *muxPackager, 64)
	go func() {
		defer close(frames)
		for {
			p := muxPack.Get()
			if _, err := p.UnPack(z); err != nil {
				return
			}
			frames <- p
		}
	}()
	m := NewMux(a, "tcp", 60, true)
	defer func() { _ = m.Close() }()
	go func() {
		_, _ = m.NewConn()
	}()
	session := func(want uint8) {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case p := <-frames:
				if p.flag != want {
					continue
				}
				out = append(out, frameVector{fmt.Sprintf("session/flag%d", want), fmt.Sprintf("%d:%x", p.id, p.content[:p.length])})
				return
			case <-deadline:
				t.Fatalf("no frame %d", want)
			}
		}
	}
	session(muxNewConn)

	b, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	const name = "testdata/frames.json"
	if os.Getenv("WIRE_GOLDEN_UPDATE") == "1" {
		if err := os.WriteFile(name, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, b) {
		t.Fatalf("mux frames differ from the recorded format\nwant %s\ngot  %s", want, b)
	}
}
