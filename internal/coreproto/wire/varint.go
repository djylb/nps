package wire

import "errors"

// MaxVarint is the largest value a QUIC variable-length integer can hold.
const MaxVarint = 1<<62 - 1

var errShort = errors.New("wire: truncated input")

// VarintLen returns the number of bytes of the shortest encoding of v.
// It panics if v exceeds MaxVarint.
func VarintLen(v uint64) int {
	switch {
	case v < 1<<6:
		return 1
	case v < 1<<14:
		return 2
	case v < 1<<30:
		return 4
	case v <= MaxVarint:
		return 8
	}
	panic("wire: varint out of range")
}

// AppendVarint appends the shortest encoding of v to b.
// It panics if v exceeds MaxVarint.
func AppendVarint(b []byte, v uint64) []byte {
	switch VarintLen(v) {
	case 1:
		return append(b, byte(v))
	case 2:
		return append(b, 0x40|byte(v>>8), byte(v))
	case 4:
		return append(b, 0x80|byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return append(b, 0xc0|byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// ReadVarint decodes a varint from the start of b and returns it with the
// number of bytes consumed. Non-shortest encodings are accepted.
func ReadVarint(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, errShort
	}
	n := 1 << (b[0] >> 6)
	if len(b) < n {
		return 0, 0, errShort
	}
	v := uint64(b[0] & 0x3f)
	for _, c := range b[1:n] {
		v = v<<8 | uint64(c)
	}
	return v, n, nil
}
