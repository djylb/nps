package session

import (
	"encoding/binary"
	"errors"
	"io"
)

// zstdFrames hands a zstd stream to a decoder one frame at a time: Read
// returns io.EOF at the end of each frame, so that the decoder, and the
// history it holds, is needed only while a frame is open. It follows only
// the frame and block headers (RFC 8878 §3.1); the decoder checks the rest.
type zstdFrames struct {
	src  io.Reader
	hdr  [18]byte // the largest header: magic, descriptor, window, dict id, size
	head []byte   // header bytes read and not yet handed on
	left int64    // bytes of the current block or checksum still to hand on
	next zstdStep
	sum  bool // the frame ends with a checksum
}

type zstdStep uint8

const (
	stepFrame zstdStep = iota // a frame header comes next
	stepBlock                 // a block header comes next
	stepSum                   // the checksum comes next
	stepEnd                   // the frame has ended
	stepSkip                  // the rest of a skippable frame
)

const (
	zstdMagic     = 0xFD2FB528
	zstdSkipMagic = 0x184D2A50 // through 0x184D2A5F
)

var errZstdFrame = errors.New("zstd: invalid frame header")

// begin starts the next frame. It returns io.EOF when the stream ends
// before one.
func (z *zstdFrames) begin() error {
	z.head, z.left, z.next = nil, 0, stepFrame
	if _, err := io.ReadFull(z.src, z.hdr[:4]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return errZstdFrame
		}
		return err
	}
	magic := binary.LittleEndian.Uint32(z.hdr[:4])
	switch {
	case magic&^0xF == zstdSkipMagic:
		if _, err := io.ReadFull(z.src, z.hdr[4:8]); err != nil {
			return noEOF(err)
		}
		z.head, z.left, z.next = z.hdr[:8], int64(binary.LittleEndian.Uint32(z.hdr[4:8])), stepSkip
		return nil
	case magic != zstdMagic:
		return errZstdFrame
	}
	if _, err := io.ReadFull(z.src, z.hdr[4:5]); err != nil {
		return noEOF(err)
	}
	fhd := z.hdr[4]
	single := fhd&0x20 != 0
	n := [4]int{0, 1, 2, 4}[fhd&3]
	if !single {
		n++
	}
	switch fhd >> 6 {
	case 0:
		if single {
			n++
		}
	case 1:
		n += 2
	case 2:
		n += 4
	case 3:
		n += 8
	}
	if _, err := io.ReadFull(z.src, z.hdr[5:5+n]); err != nil {
		return noEOF(err)
	}
	z.head, z.next, z.sum = z.hdr[:5+n], stepBlock, fhd&4 != 0
	return nil
}

func (z *zstdFrames) Read(p []byte) (int, error) {
	for {
		if len(z.head) > 0 {
			n := copy(p, z.head)
			z.head = z.head[n:]
			return n, nil
		}
		if z.left > 0 {
			n, err := z.src.Read(p[:min(int64(len(p)), z.left)])
			z.left -= int64(n)
			if err != nil && (z.left > 0 || !errors.Is(err, io.EOF)) {
				return n, noEOF(err)
			}
			return n, nil
		}
		switch z.next {
		case stepBlock:
			if _, err := io.ReadFull(z.src, z.hdr[:3]); err != nil {
				return 0, noEOF(err)
			}
			h := uint32(z.hdr[0]) | uint32(z.hdr[1])<<8 | uint32(z.hdr[2])<<16
			size := int64(h >> 3)
			switch (h >> 1) & 3 {
			case 1: // RLE: one byte
				size = 1
			case 3:
				return 0, errZstdFrame
			}
			z.head, z.left = z.hdr[:3], size
			if h&1 != 0 {
				z.next = stepSum
			}
		case stepSum:
			z.next = stepEnd
			if z.sum {
				z.left = 4
			}
		case stepSkip:
			z.next = stepEnd
		default:
			return 0, io.EOF
		}
	}
}

// noEOF makes an end of the stream inside a frame a truncation.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
