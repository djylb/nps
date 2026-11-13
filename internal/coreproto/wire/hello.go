package wire

import (
	"errors"
	"net/netip"
)

// Hello is the application data of msg2 and msg3 (§3.5). Only C=0 fields are
// allowed in it.
type Hello struct {
	Caps         []uint64
	CompressAlgs []uint64
	Label, Agent string // msg3 only
	// VisitorOnly is a client session that hosts nothing: it only makes
	// links to providers and is never a node of its client.
	VisitorOnly bool
	// LocalAddr is the IP address the client dials from; "" when it does not
	// say. It is shown, never used to route.
	LocalAddr string
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
	if h.VisitorOnly {
		b = AppendUint(b, KeyVisitorOnly, 1)
	}
	if h.LocalAddr != "" {
		b = AppendBytes(b, KeyLocalAddr, clip(h.LocalAddr))
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
		case KeyVisitorOnly:
			h.VisitorOnly = f.Value == 1
		case KeyLocalAddr:
			// Anything but a bare address (zone dropped) is not kept.
			if a, err := netip.ParseAddr(string(f.Data)); err == nil && len(f.Data) <= MaxLabel {
				h.LocalAddr = a.WithZone("").Unmap().String()
			}
		}
		return nil
	})
	if errors.Is(err, ErrUnsupported) {
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
