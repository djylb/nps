package wire

import "errors"

// Errors of decoding. ErrMalformed answers with NPS_MALFORMED and
// ErrUnsupported (an unknown C=1 field) with NPS_UNSUPPORTED.
var (
	ErrMalformed   = errors.New("wire: malformed")
	ErrUnsupported = errors.New("wire: unsupported critical field")
)

// Key is an encoded field key: id·4 + C·2 + W.
type Key uint64

// ID returns the field id.
func (k Key) ID() uint64 { return uint64(k) >> 2 }

// Critical reports whether a receiver that does not know the id must refuse.
func (k Key) Critical() bool { return k&2 != 0 }

// IsBytes reports whether the value is length-prefixed bytes (W = 1).
func (k Key) IsBytes() bool { return k&1 != 0 }

func key(id uint64, critical, isBytes bool) Key {
	k := id << 2
	if critical {
		k |= 2
	}
	if isBytes {
		k |= 1
	}
	return Key(k)
}

// grease reports the ids reserved for test injection, skipped as C=0.
func grease(id uint64) bool { return id%16 == 15 }

// Field is one decoded field. Data aliases the input.
type Field struct {
	Key   Key
	Value uint64 // when !Key.IsBytes()
	Data  []byte // when Key.IsBytes()
}

// spec is one row of a table of fields.
type spec struct {
	key      Key
	repeated bool
}

// Schema is a table of the fields of one id space.
type Schema map[uint64]spec

func newSchema(rows ...spec) Schema {
	m := make(Schema, len(rows))
	for _, r := range rows {
		m[r.key.ID()] = r
	}
	return m
}

func one(k Key) spec  { return spec{key: k} }
func many(k Key) spec { return spec{key: k, repeated: true} }

// Decode walks Field* in b and calls visit for every known field: id 0, a
// known id with the wrong C or W, and a non-repeated id seen twice are
// ErrMalformed, an unknown C=1 field is ErrUnsupported, and unknown C=0 and
// GREASE fields are skipped.
func (s Schema) Decode(b []byte, visit func(Field) error) error {
	var seen []uint64
	for len(b) > 0 {
		f, n, err := readField(b)
		if err != nil {
			return err
		}
		b = b[n:]
		id := f.Key.ID()
		row, known := s[id]
		if !known {
			if f.Key.Critical() && !grease(id) {
				return ErrUnsupported
			}
			continue
		}
		if row.key != f.Key {
			return ErrMalformed
		}
		if !row.repeated {
			for _, v := range seen {
				if v == id {
					return ErrMalformed
				}
			}
			seen = append(seen, id)
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}

// readField reads one field from the start of b.
func readField(b []byte) (Field, int, error) {
	k, n, err := ReadVarint(b)
	if err != nil || k>>2 == 0 {
		return Field{}, 0, ErrMalformed
	}
	v, m, err := ReadVarint(b[n:])
	if err != nil {
		return Field{}, 0, ErrMalformed
	}
	n += m
	f := Field{Key: Key(k)}
	if !f.Key.IsBytes() {
		f.Value = v
		return f, n, nil
	}
	if v > uint64(len(b)-n) {
		return Field{}, 0, ErrMalformed
	}
	f.Data = b[n : n+int(v)]
	return f, n + int(v), nil
}

// AppendUint appends a varint-valued field.
func AppendUint(b []byte, k Key, v uint64) []byte {
	return AppendVarint(AppendVarint(b, uint64(k)), v)
}

// AppendBytes appends a bytes-valued field.
func AppendBytes(b []byte, k Key, d []byte) []byte {
	b = AppendVarint(AppendVarint(b, uint64(k)), uint64(len(d)))
	return append(b, d...)
}
