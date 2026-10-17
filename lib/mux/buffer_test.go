package mux

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMuxReleaseReturnsEachBufferOnce(t *testing.T) {
	m := &Mux{closeChan: make(chan struct{})}
	m.writeQueue.New()
	m.newConnQueue.New()
	const packs = 64
	for i := 0; i < packs; i++ {
		pack := muxPack.Get()
		if err := pack.Set(muxNewMsg, 1, []byte("payload")); err != nil {
			t.Fatalf("Set() error = %v", err)
		}
		pack.content = windowBuff.Get()
		m.writeQueue.Push(pack)
	}
	m.release()

	seen := make(map[*byte]struct{})
	for i := 0; i < 4*packs; i++ {
		b := windowBuff.Get()
		p := &b[0]
		if _, dup := seen[p]; dup {
			t.Fatal("pool handed out the same buffer twice")
		}
		seen[p] = struct{}{}
	}
}

func TestPingReturnPayloadDoesNotLeakPoolData(t *testing.T) {
	for _, length := range []uint16{8, 9, 12} {
		for i := 0; i < 64; i++ {
			pack := &muxPackager{}
			pack.content = make([]byte, poolSizeWindowBuffer)
			for j := range pack.content {
				pack.content[j] = 0xAA // stale data left by a previous user
			}
			binary.BigEndian.PutUint64(pack.content[:8], 0x0102030405060708)
			clear(pack.content[8:length])
			pack.length = length

			out := pingReturnPayload(pack)
			if len(out) < 8 || binary.BigEndian.Uint64(out[:8]) != 0x0102030405060708 {
				t.Fatalf("length %d: timestamp not echoed", length)
			}
			if !isZero(out[8:]) {
				t.Fatalf("length %d: reply padding carries stale bytes: %x", length, out[8:])
			}
		}
	}

	// non-zero padding is echoed as received, never extended
	pack := &muxPackager{}
	pack.content = make([]byte, poolSizeWindowBuffer)
	for j := range pack.content {
		pack.content[j] = 0xAA
	}
	pack.length = 10
	if out := pingReturnPayload(pack); len(out) != 10 {
		t.Fatalf("reply length = %d, want 10", len(out))
	}
}

type scriptedConn struct {
	net.Conn
	write func(p []byte) (int, error)
}

func (c *scriptedConn) Write(p []byte) (int, error) { return c.write(p) }

func TestFlushWriterStickyError(t *testing.T) {
	writeErr := errors.New("broken pipe")
	calls := 0
	fw := NewFlushWriter(&scriptedConn{write: func(p []byte) (int, error) {
		calls++
		return 0, writeErr
	}})
	if _, err := fw.Write([]byte("frame")); err != nil {
		t.Fatalf("buffered Write() error = %v", err)
	}
	if err := fw.Flush(); !errors.Is(err, writeErr) {
		t.Fatalf("Flush() error = %v, want %v", err, writeErr)
	}
	if _, err := fw.Write([]byte("next")); !errors.Is(err, writeErr) {
		t.Fatalf("Write() after failure error = %v, want sticky %v", err, writeErr)
	}
	if err := fw.Flush(); !errors.Is(err, writeErr) {
		t.Fatalf("second Flush() error = %v, want sticky %v", err, writeErr)
	}
	big := make([]byte, poolSizeWindowBuffer+1)
	if _, err := fw.Write(big); !errors.Is(err, writeErr) {
		t.Fatalf("large Write() error = %v, want sticky %v", err, writeErr)
	}
	_ = fw.Close()
	if calls != 1 {
		t.Fatalf("underlying Write called %d times, want 1", calls)
	}
}

func TestFlushWriterHandlesShortWrites(t *testing.T) {
	var got []byte
	fw := NewFlushWriter(&scriptedConn{write: func(p []byte) (int, error) {
		n := min(len(p), 3)
		got = append(got, p[:n]...)
		return n, nil
	}})
	want := "a frame larger than one short write"
	if _, err := fw.Write([]byte(want)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := fw.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("written %q, want %q", got, want)
	}

	stalled := NewFlushWriter(&scriptedConn{write: func(p []byte) (int, error) { return 0, nil }})
	_, _ = stalled.Write([]byte("x"))
	if err := stalled.Flush(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Flush() without progress error = %v, want io.ErrShortWrite", err)
	}
}

func TestMuxClosesOnUnderlyingWriteFailure(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	failing := &scriptedConn{Conn: a, write: func(p []byte) (int, error) {
		return 0, errors.New("connection reset by peer")
	}}
	m := NewMux(failing, "tcp", 30, true)
	defer func() { _ = m.Close() }()

	go func() { _, _ = m.NewConn() }()
	deadline := time.Now().Add(5 * time.Second)
	for !m.IsClosed() {
		if time.Now().After(deadline) {
			t.Fatal("mux not closed after underlying write failure")
		}
		time.Sleep(time.Millisecond)
	}
	if reason := m.CloseReason(); !strings.Contains(reason, "write session") {
		t.Fatalf("CloseReason() = %q, want write session failure", reason)
	}
}
