package mux

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReceivedPayloadOwnership(t *testing.T) {
	for _, flag := range []uint8{muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn} {
		for _, truncated := range []bool{false, true} {
			wire := make([]byte, 10)
			wire[0] = flag
			binary.LittleEndian.PutUint16(wire[5:7], 3)
			copy(wire[7:], "abc")
			if truncated {
				wire = wire[:9]
			}
			pack := muxPack.Get()
			_, err := pack.UnPack(bytes.NewReader(wire))
			if (err != nil) != truncated {
				t.Fatalf("flag %d truncated %v: %v", flag, truncated, err)
			}
			if pack.content == nil {
				t.Fatal("payload ownership lost before disposal")
			}
			pack.reset() // Also covers discarded messages for nonexistent streams.
			if pack.content != nil || pack.buf != nil {
				t.Fatal("disposed packet retains buffers")
			}
			pack.reset() // Repeated disposal must not return a buffer twice.
			muxPack.Put(pack)
		}
	}
}

func TestMessageTransfersPayloadToWindow(t *testing.T) {
	m := &Mux{closeChan: make(chan struct{}), bw: new(Bandwidth)}
	m.writeQueue.New()
	m.newConnQueue.New()
	defer m.release()
	c := NewConn(1, m)
	defer c.receiveWindow.CloseWindow()
	pack := muxPack.Get()
	pack.flag = muxNewMsg
	pack.content = windowBuff.Get()
	pack.length = 3
	copy(pack.content, "abc")
	original := pack.content
	if err := m.newMsg(c, pack); err != nil {
		t.Fatal(err)
	}
	if pack.content != nil {
		t.Fatal("packet still owns transferred payload")
	}
	muxPack.Put(pack)
	ele := c.receiveWindow.bufQueue.TryPop()
	if ele == nil || &ele.Buf[0] != &original[0] || string(ele.Buf[:ele.L]) != "abc" {
		t.Fatal("window did not retain original payload")
	}
	windowBuff.Put(ele.Buf)
	listEle.Put(ele)
}

func TestClosedWindowKeepsPayloadWithPacket(t *testing.T) {
	m := &Mux{closeChan: make(chan struct{}), bw: new(Bandwidth)}
	c := NewConn(1, m)
	c.receiveWindow.CloseWindow()
	pack := muxPack.Get()
	pack.flag = muxNewMsg
	pack.content = windowBuff.Get()
	pack.length = 3
	if err := m.newMsg(c, pack); err == nil {
		t.Fatal("closed window accepted payload")
	}
	if pack.content == nil {
		t.Fatal("failed transfer lost payload")
	}
	muxPack.Put(pack)
}
