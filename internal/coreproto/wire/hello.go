package wire

// Hello is the application data of msg2 and msg3 (§3.5). Only C=0 fields are
// allowed in it.
type Hello struct {
	Caps         []uint64
	CompressAlgs []uint64
	Label, Agent string // msg3 only
}

func clip(s string) []byte {
	if len(s) > MaxLabel {
		s = s[:MaxLabel]
	}
	return []byte(s)
}

// AppendHello appends h in id order.
func AppendHello(b []byte, h *Hello) []byte {
	for _, c := range h.Caps {
		b = AppendUint(b, KeyCap, c)
	}
	if h.Label != "" {
		b = AppendBytes(b, KeyLabel, clip(h.Label))
	}
	if h.Agent != "" {
		b = AppendBytes(b, KeyAgent, clip(h.Agent))
	}
	for _, a := range h.CompressAlgs {
		b = AppendUint(b, KeyCompressAlg, a)
	}
	return b
}

// ParseHello parses Hello bytes; any C=1 field is malformed.
func ParseHello(b []byte) (Hello, error) {
	var h Hello
	err := Common.Decode(b, func(f Field) error {
		if f.Key.Critical() {
			return ErrMalformed
		}
		switch f.Key {
		case KeyCap:
			h.Caps = append(h.Caps, f.Value)
		case KeyCompressAlg:
			h.CompressAlgs = append(h.CompressAlgs, f.Value)
		case KeyLabel:
			if len(f.Data) <= MaxLabel {
				h.Label = string(f.Data)
			}
		case KeyAgent:
			if len(f.Data) <= MaxLabel {
				h.Agent = string(f.Data)
			}
		}
		return nil
	})
	if err == ErrUnsupported {
		err = ErrMalformed
	}
	return h, err
}

// HasCap reports whether h advertises c.
func (h *Hello) HasCap(c uint64) bool {
	for _, v := range h.Caps {
		if v == c {
			return true
		}
	}
	return false
}
