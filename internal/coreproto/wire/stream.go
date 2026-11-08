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

// session_options fields (§5).
var (
	keySoResumeMs = key(1, false, false)
	keySoIdleS    = key(2, false, false)
	keySoDeadS    = key(3, false, false)
)

var sessionOptionsSchema = newSchema(one(keySoResumeMs), one(keySoIdleS), one(keySoDeadS))

// Record modes of CONFIG `record` (§5.1); 0 is "absent".
const (
	RecordDefault       uint64 = 0
	RecordBound         uint64 = 1
	RecordSealed        uint64 = 2
	RecordBoundOrSealed uint64 = 3
	RecordTrusted       uint64 = 4
)

// SessionOptions is the session_options section of a CONFIG snapshot; a zero
// field is absent.
type SessionOptions struct {
	ResumeMs, IdleS, DeadS uint64
}

// Snapshot is the part of a CONFIG snapshot in use so far.
type Snapshot struct {
	Session  SessionOptions
	Record   uint64
	Compress uint64 // the client's default algorithm, 0 for none
	// SchedulerUp is the sending scheduler of npc (§5), HasSchedulerUp
	// false when absent: npc then keeps its local one.
	SchedulerUp    uint64
	HasSchedulerUp bool
}

// AppendSnapshot appends the fields of s, default values omitted.
func AppendSnapshot(b []byte, s *Snapshot) []byte {
	var so []byte
	if s.Session.ResumeMs != 0 {
		so = AppendUint(so, keySoResumeMs, s.Session.ResumeMs)
	}
	if s.Session.IdleS != 0 {
		so = AppendUint(so, keySoIdleS, s.Session.IdleS)
	}
	if s.Session.DeadS != 0 {
		so = AppendUint(so, keySoDeadS, s.Session.DeadS)
	}
	if len(so) > 0 {
		b = AppendBytes(b, keyCfgSessionOptions, so)
	}
	if s.HasSchedulerUp {
		b = AppendUint(b, keyCfgSchedulerUp, s.SchedulerUp)
	}
	if s.Record != RecordDefault {
		b = AppendUint(b, keyCfgRecord, s.Record)
	}
	if s.Compress != 0 {
		b = AppendUint(b, keyCfgCompress, s.Compress)
	}
	return b
}

// ParseSnapshot parses a snapshot; an unknown record or compress value is the
// default (C=0, §1).
func ParseSnapshot(b []byte) (Snapshot, error) {
	var s Snapshot
	err := configSchema.Decode(b, func(f Field) error {
		switch f.Key {
		case keyCfgSchedulerUp:
			if f.Value <= SchedulerSpreadRateWeighted {
				s.SchedulerUp, s.HasSchedulerUp = f.Value, true
			}
		case keyCfgRecord:
			if f.Value >= RecordBound && f.Value <= RecordTrusted {
				s.Record = f.Value
			}
		case keyCfgCompress:
			if KnownCompress(f.Value) {
				s.Compress = f.Value
			}
		case keyCfgSessionOptions:
			return sessionOptionsSchema.Decode(f.Data, func(o Field) error {
				switch o.Key {
				case keySoResumeMs:
					s.Session.ResumeMs = o.Value
				case keySoIdleS:
					s.Session.IdleS = o.Value
				case keySoDeadS:
					s.Session.DeadS = o.Value
				}
				return nil
			})
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

// StreamOpen is the OPEN_INFO of a TCP or UDP stream (§4.4, §4.5).
type StreamOpen struct {
	Op        Op
	Target    *Address
	Source    *Address
	WantReply bool
	Compress  uint64
	Service   []byte
	LinkID    uint64
}

// AppendStreamOpen encodes o in id order; the result is at most MaxOpenInfo
// bytes or the error is ErrMalformed.
func AppendStreamOpen(o *StreamOpen) ([]byte, error) {
	b := AppendOpenInfo(nil, o.Op)
	addr := func(k Key, a *Address) error {
		if a == nil {
			return nil
		}
		v, err := AppendAddress(nil, *a)
		if err != nil {
			return err
		}
		b = AppendBytes(b, k, v)
		return nil
	}
	if err := addr(KeyTarget, o.Target); err != nil {
		return nil, err
	}
	if err := addr(KeySource, o.Source); err != nil {
		return nil, err
	}
	if o.WantReply {
		b = AppendUint(b, KeyWantReply, 1)
	}
	if o.Compress != 0 {
		b = AppendUint(b, KeyCompress, o.Compress)
	}
	if len(o.Service) > 0 {
		b = AppendBytes(b, KeyService, o.Service)
	}
	if o.LinkID != 0 {
		b = AppendUint(b, KeyLinkID, o.LinkID)
	}
	if len(b) > MaxOpenInfo {
		return nil, ErrMalformed
	}
	return b, nil
}

// ParseStreamOpen decodes the fields of a TCP or UDP OPEN_INFO and checks its
// shape: a TCP stream has exactly one of target and service, and a UDP stream
// has no service and no compress (§7). A compress value this side does not
// know is left to the caller, which answers NPS_UNSUPPORTED.
func ParseStreamOpen(op Op, fields []byte) (StreamOpen, error) {
	o := StreamOpen{Op: op}
	addr := func(d []byte) (*Address, error) {
		a, err := ParseAddress(d)
		if err != nil {
			return nil, err
		}
		return &a, nil
	}
	err := Common.Decode(fields, func(f Field) error {
		var err error
		switch f.Key {
		case KeyTarget:
			if o.Target != nil {
				return ErrMalformed
			}
			o.Target, err = addr(f.Data)
		case KeySource:
			o.Source, err = addr(f.Data)
		case KeyWantReply:
			o.WantReply = f.Value == 1
		case KeyCompress:
			if f.Value == 0 {
				return ErrMalformed
			}
			o.Compress = f.Value
		case KeyService:
			o.Service = f.Data
		case KeyLinkID:
			o.LinkID = f.Value
		}
		return err
	})
	if err != nil {
		return o, err
	}
	if o.Target != nil && o.Target.Port == 0 {
		return o, ErrMalformed
	}
	switch op {
	case OpTCP:
		if (o.Target == nil) == (len(o.Service) == 0) {
			return o, ErrMalformed
		}
	case OpUDP:
		if o.Compress != 0 || len(o.Service) > 0 {
			return o, ErrMalformed
		}
	}
	return o, nil
}

// MaxUDPPacket bounds one packet of a UDP stream.
const MaxUDPPacket = 1<<16 + 300

// MaxUDPPayload bounds the Payload of one packet; a longer one is
// NPS_MALFORMED (§4.5).
const MaxUDPPayload = 65535

// Values of scheduler_up (§5).
const (
	SchedulerAffinity uint64 = iota
	SchedulerSessionAffinity
	SchedulerSpreadETA
	SchedulerSpreadRoundRobin
	SchedulerSpreadRateWeighted
)

// AppendUDPBody appends what follows Len of an addressed packet (§4.5):
// AddrLen Address Payload. A nil a is a fixed-target packet, Payload alone.
func AppendUDPBody(b []byte, a *Address, payload []byte) ([]byte, error) {
	if a != nil {
		v, err := AppendAddress(nil, *a)
		if err != nil {
			return b, err
		}
		b = append(AppendVarint(b, uint64(len(v))), v...)
	}
	return append(b, payload...), nil
}

// ParseUDPBody splits an addressed packet into its address and payload.
func ParseUDPBody(b []byte) (Address, []byte, error) {
	n, m, err := ReadVarint(b)
	if err != nil || n > uint64(len(b)-m) {
		return Address{}, nil, ErrMalformed
	}
	a, err := ParseAddress(b[m : m+int(n)])
	if err != nil {
		return Address{}, nil, err
	}
	return a, b[m+int(n):], nil
}

// ReadVarintFrom reads one varint from r.
func ReadVarintFrom(r io.Reader) (uint64, error) { return readVarintFrom(r) }
