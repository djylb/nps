package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/klauspost/compress/zstd"
)

// readFrames decodes src frame by frame as compressConn does.
func readFrames(src []byte) ([]byte, int, error) {
	zf := &zstdFrames{src: bytes.NewReader(src)}
	var out bytes.Buffer
	frames := 0
	for {
		if err := zf.begin(); err != nil {
			if err == io.EOF {
				return out.Bytes(), frames, nil
			}
			return nil, frames, err
		}
		d, err := getZstdDecoder(zf, DefaultCompressMaxWindow)
		if err != nil {
			return nil, frames, err
		}
		if _, err := io.Copy(&out, d); err != nil {
			return nil, frames, err
		}
		putZstdDecoder(d, DefaultCompressMaxWindow)
		frames++
	}
}

func TestZstdFramesSplitsEveryKind(t *testing.T) {
	data := bytes.Repeat([]byte("frames of zstd "), 30000)
	var src, want bytes.Buffer
	for _, opts := range [][]zstd.EOption{
		{zstd.WithEncoderCRC(true)},
		{zstd.WithEncoderCRC(false), zstd.WithSingleSegment(true)},
		{zstd.WithSingleSegment(false), zstd.WithWindowSize(1 << 10)},
		{zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithZeroFrames(true)},
	} {
		e, err := zstd.NewWriter(nil, opts...)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(e.EncodeAll(data, nil))
		src.Write(e.EncodeAll(nil, nil)) // empty frame
		want.Write(data)
		_ = e.Close()
	}
	// A skippable frame is passed over.
	skip := binary.LittleEndian.AppendUint32(nil, zstdSkipMagic|3)
	skip = binary.LittleEndian.AppendUint32(skip, 5)
	src.Write(append(skip, "abcde"...))
	// Streaming with flushes: one frame of many blocks.
	var sw bytes.Buffer
	e, _ := zstd.NewWriter(&sw, zstd.WithWindowSize(zstdSendWindow))
	for range 5 {
		_, _ = e.Write(data[:7000])
		_ = e.Flush()
		want.Write(data[:7000])
	}
	_ = e.Close()
	src.Write(sw.Bytes())

	got, frames, err := readFrames(src.Bytes())
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("%d bytes, err %v", len(got), err)
	}
	if frames != 10 {
		t.Fatalf("frames %d, want 10", frames)
	}
	// Every cut inside a frame is a truncation, not a clean end.
	whole := src.Bytes()
	for _, cut := range []int{1, 4, 5, 9, 100, len(whole) - 1} {
		if _, _, err := readFrames(whole[:cut]); err == nil {
			t.Fatalf("cut at %d decoded cleanly", cut)
		}
	}
	if _, _, err := readFrames([]byte{1, 2, 3, 4}); !errors.Is(err, errZstdFrame) {
		t.Fatalf("bad magic: %v", err)
	}
}

// An idle stream ends its frame and gives its encoder back; it goes on in a
// new frame afterwards.
func TestCompressedIdleEndsFrame(t *testing.T) {
	l := startLink(t, DialConfig{})
	target := listenEcho(t)
	c, err := l.server.OpenLink(bg(t), coreapi.LinkRequest{Network: coreapi.NetworkTCP, Target: target, Compress: []string{"zstd"}, WantReply: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	cc := c.(*compressConn)
	msg := bytes.Repeat([]byte("idle then busy "), 3000)
	roundTrip(t, c, msg)
	held := func() bool {
		cc.wmu.Lock()
		defer cc.wmu.Unlock()
		return cc.enc != nil
	}
	if !held() {
		t.Fatal("no encoder while the frame is open")
	}
	deadline := time.Now().Add(3 * compressIdle)
	for held() {
		if time.Now().After(deadline) {
			t.Fatal("the idle frame did not end")
		}
		time.Sleep(50 * time.Millisecond)
	}
	roundTrip(t, c, msg)
	if err := cc.CloseWrite(); err != nil {
		t.Fatal(err)
	}
}
