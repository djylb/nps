package wire

import "io"

// AppendOpenInfo starts OPEN_INFO = Op Field* (§4.1): the fields follow,
// appended in id order, and the result is at most MaxOpenInfo bytes.
func AppendOpenInfo(b []byte, op Op) []byte { return AppendVarint(b, uint64(op)) }

// ParseOpenOp splits OPEN_INFO into its Op and the bytes of its fields.
func ParseOpenOp(b []byte) (Op, []byte, error) {
	op, n, err := ReadVarint(b)
	if err != nil {
		return 0, nil, ErrMalformed
	}
	return Op(op), b[n:], nil
}

// Reply is a decoded reply head (§4.2).
type Reply struct {
	Code   uint64
	Reason string
	Fields []byte // every field, for the fields of the Op
}

// AppendReply appends a reply head; on success with no fields it is the one
// byte 00.
func AppendReply(b []byte, code uint64, reason string, fields []byte) []byte {
	var body []byte
	if code != CodeOK {
		body = AppendUint(body, KeyCode, code)
	}
	if reason != "" {
		body = AppendBytes(body, KeyReason, []byte(reason))
	}
	body = append(body, fields...)
	b = AppendVarint(b, uint64(len(body)))
	return append(b, body...)
}

// ReadReply reads one reply head of at most limit bytes from r.
func ReadReply(r io.Reader, limit int) (Reply, error) {
	n, err := readVarintFrom(r)
	if err != nil {
		return Reply{}, err
	}
	if n > uint64(limit) {
		return Reply{}, ErrMalformed
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Reply{}, err
	}
	rep := Reply{Fields: body}
	err = Common.Decode(body, func(f Field) error {
		switch f.Key {
		case KeyCode:
			rep.Code = f.Value
		case KeyReason:
			rep.Reason = string(f.Data)
		}
		return nil
	})
	return rep, err
}

func readVarintFrom(r io.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return 0, err
	}
	n := 1 << (b[0] >> 6)
	if n > 1 {
		if _, err := io.ReadFull(r, b[1:n]); err != nil {
			return 0, err
		}
	}
	v, _, err := ReadVarint(b[:n])
	return v, err
}

// CONFIG snapshot fields (§5, an id space of its own).
var (
	keyCfgCarrier        = key(1, false, true)
	keyCfgMaxPipes       = key(2, false, false)
	keyCfgSchedulerUp    = key(3, false, false)
	keyCfgSessionOptions = key(4, false, true)
	keyCfgListener       = key(5, false, true)
	keyCfgShaping        = key(6, false, true)
	keyCfgHealth         = key(7, false, true)
	keyCfgRecord         = key(8, false, false)
	keyCfgCompress       = key(9, false, false)
)

var configSchema = newSchema(
	many(keyCfgCarrier), one(keyCfgMaxPipes), one(keyCfgSchedulerUp), one(keyCfgSessionOptions),
	many(keyCfgListener), one(keyCfgShaping), many(keyCfgHealth), one(keyCfgRecord), one(keyCfgCompress),
)

// Record modes of CONFIG `record` (§5.1); 0 is "absent".
const (
	RecordDefault       uint64 = 0
	RecordBound         uint64 = 1
	RecordSealed        uint64 = 2
	RecordBoundOrSealed uint64 = 3
	RecordTrusted       uint64 = 4
)

// Snapshot is the part of a CONFIG snapshot in use so far.
type Snapshot struct {
	Record uint64
}

// AppendSnapshot appends the fields of s, default values omitted.
func AppendSnapshot(b []byte, s *Snapshot) []byte {
	if s.Record != RecordDefault {
		b = AppendUint(b, keyCfgRecord, s.Record)
	}
	return b
}

// ParseSnapshot parses a snapshot; an unknown record value is the default.
func ParseSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	err := configSchema.Decode(b, func(f Field) error {
		if f.Key == keyCfgRecord && f.Value >= RecordBound && f.Value <= RecordTrusted {
			s.Record = f.Value
		}
		return nil
	})
	return s, err
}

// AppendConfigOpen is the OPEN_INFO of a CONFIG push: Op seq.
func AppendConfigOpen(seq uint64) []byte {
	return AppendUint(AppendOpenInfo(nil, OpConfig), KeySeq, seq)
}

// ParseConfigOpen returns the seq of CONFIG's fields; 0 is malformed.
func ParseConfigOpen(fields []byte) (uint64, error) {
	var seq uint64
	err := Common.Decode(fields, func(f Field) error {
		if f.Key == KeySeq {
			seq = f.Value
		}
		return nil
	})
	if err == nil && seq == 0 {
		err = ErrMalformed
	}
	return seq, err
}
