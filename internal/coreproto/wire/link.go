package wire

// Connection modes of a LINK (§2.2).
const (
	ConnRelay  uint64 = 1
	ConnAuto   uint64 = 2
	ConnDirect uint64 = 3
)

// Target types of a LINK (§2.2).
const (
	TargetTCP uint64 = 1
	TargetUDP uint64 = 2
	TargetAll uint64 = 3
)

// MaxLinkBody is the default bound of a LINK body (§9.1).
const MaxLinkBody = 16 << 20

// LinkOpen is the OPEN_INFO of a visitor's LINK (§9.1).
type LinkOpen struct {
	Tunnel   uint64
	Access   []byte
	ConnMode uint64 // 0: the default
	// ServerExit asks for the server as the exit of the link; the reply
	// says whether the tunnel lets it.
	ServerExit bool
	// RelayWatch asks for relay state heads after the reply (KeyRelayWatch).
	RelayWatch bool
}

// AppendLinkOpen encodes o.
func AppendLinkOpen(o *LinkOpen) ([]byte, error) {
	b := AppendOpenInfo(nil, OpLink)
	if o.Tunnel != 0 {
		b = AppendUint(b, KeyTunnel, o.Tunnel)
	}
	if len(o.Access) > 0 {
		b = AppendBytes(b, KeyAccess, o.Access)
	}
	if o.ConnMode != 0 {
		b = AppendUint(b, KeyConnMode, o.ConnMode)
	}
	if o.ServerExit {
		b = AppendUint(b, KeyServerExit, 1)
	}
	if o.RelayWatch {
		b = AppendUint(b, KeyRelayWatch, 1)
	}
	if len(b) > MaxOpenInfo {
		return nil, ErrMalformed
	}
	return b, nil
}

// ParseLinkOpen decodes the fields of a visitor's LINK: exactly one of tunnel
// and access.
func ParseLinkOpen(fields []byte) (LinkOpen, error) {
	var o LinkOpen
	err := Common.Decode(fields, func(f Field) error {
		switch f.Key {
		case KeyTunnel:
			o.Tunnel = f.Value
		case KeyAccess:
			o.Access = f.Data
		case KeyConnMode:
			o.ConnMode = f.Value
		case KeyServerExit:
			o.ServerExit = f.Value == 1
		case KeyRelayWatch:
			o.RelayWatch = f.Value == 1
		}
		return nil
	})
	if err != nil {
		return o, err
	}
	if (o.Tunnel == 0) == (len(o.Access) == 0) || o.ConnMode > ConnDirect {
		return o, ErrMalformed
	}
	return o, nil
}

// LinkReply is what the reply head of a visitor's LINK says (§9.1).
type LinkReply struct {
	LinkID     uint64
	LinkKey    []byte
	ConnMode   uint64
	ServerExit bool
	// NoRelay: the server refuses the link's PIPE; only a direct path works.
	NoRelay bool
}

// AppendLinkReply encodes the fields of a successful reply head.
func AppendLinkReply(r *LinkReply) []byte {
	var b []byte
	b = AppendUint(b, KeyLinkID, r.LinkID)
	if len(r.LinkKey) > 0 {
		b = AppendBytes(b, KeyLinkKey, r.LinkKey)
	}
	if r.ConnMode != 0 {
		b = AppendUint(b, KeyConnMode, r.ConnMode)
	}
	if r.ServerExit {
		b = AppendUint(b, KeyServerExit, 1)
	}
	if r.NoRelay {
		b = AppendUint(b, KeyNoRelay, 1)
	}
	return b
}

// ParseLinkReply decodes the fields of a successful reply head.
func ParseLinkReply(fields []byte) (LinkReply, error) {
	var r LinkReply
	err := Common.Decode(fields, func(f Field) error {
		switch f.Key {
		case KeyLinkID:
			r.LinkID = f.Value
		case KeyLinkKey:
			r.LinkKey = f.Data
		case KeyConnMode:
			r.ConnMode = f.Value
		case KeyServerExit:
			r.ServerExit = f.Value == 1
		case KeyNoRelay:
			r.NoRelay = f.Value == 1
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	if r.LinkID == 0 || !r.ServerExit && len(r.LinkKey) != 32 {
		return r, ErrMalformed
	}
	return r, nil
}

// LinkBody is the body of the LINK the server opens to the provider (§9.1).
type LinkBody struct {
	LinkKey    []byte
	ConnMode   uint64
	Targets    []Address
	TargetType uint64 // 0: all
	NameDest   bool
	Scope      string // "": everything
}

// AppendLinkBody encodes b as Len Field*.
func AppendLinkBody(body *LinkBody) ([]byte, error) {
	var f []byte
	for _, t := range body.Targets {
		v, err := AppendAddress(nil, t)
		if err != nil {
			return nil, err
		}
		f = AppendBytes(f, KeyTarget, v)
	}
	f = AppendBytes(f, KeyLinkKey, body.LinkKey)
	if body.ConnMode != 0 {
		f = AppendUint(f, KeyConnMode, body.ConnMode)
	}
	if body.Scope != "" {
		f = AppendBytes(f, KeyScope, []byte(body.Scope))
	}
	if body.NameDest {
		f = AppendUint(f, KeyNameDest, 1)
	}
	if body.TargetType != 0 {
		f = AppendUint(f, KeyTargetType, body.TargetType)
	}
	return append(AppendVarint(nil, uint64(len(f))), f...), nil
}

// ParseLinkBody decodes the fields of a LINK body.
func ParseLinkBody(fields []byte) (LinkBody, error) {
	var b LinkBody
	err := Common.Decode(fields, func(f Field) error {
		switch f.Key {
		case KeyTarget:
			a, err := ParseAddress(f.Data)
			if err != nil {
				return err
			}
			b.Targets = append(b.Targets, a)
		case KeyLinkKey:
			b.LinkKey = f.Data
		case KeyConnMode:
			b.ConnMode = f.Value
		case KeyTargetType:
			b.TargetType = f.Value
		case KeyNameDest:
			b.NameDest = f.Value == 1
		case KeyScope:
			b.Scope = string(f.Data)
		}
		return nil
	})
	if err != nil {
		return b, err
	}
	if len(b.LinkKey) != 32 || b.TargetType > TargetAll {
		return b, ErrMalformed
	}
	return b, nil
}

// PathOpen is the OPEN_INFO of a PIPE or PUNCH stream (§9.2, §9.4).
type PathOpen struct {
	Op         Op
	LinkID     uint64
	Background bool
	ServerAddr string
}

// AppendPathOpen encodes o.
func AppendPathOpen(o *PathOpen) ([]byte, error) {
	b := AppendOpenInfo(nil, o.Op)
	b = AppendUint(b, KeyLinkID, o.LinkID)
	if o.Background {
		b = AppendUint(b, KeyBackground, 1)
	}
	if o.ServerAddr != "" {
		b = AppendBytes(b, KeyServerAddr, []byte(o.ServerAddr))
	}
	if len(b) > MaxOpenInfo {
		return nil, ErrMalformed
	}
	return b, nil
}

// ParsePathOpen decodes the fields of a PIPE or PUNCH stream.
func ParsePathOpen(op Op, fields []byte) (PathOpen, error) {
	o := PathOpen{Op: op}
	err := Common.Decode(fields, func(f Field) error {
		switch f.Key {
		case KeyLinkID:
			o.LinkID = f.Value
		case KeyBackground:
			o.Background = f.Value == 1
		case KeyServerAddr:
			o.ServerAddr = string(f.Data)
		}
		return nil
	})
	if err == nil && o.LinkID == 0 {
		err = ErrMalformed
	}
	return o, err
}

// AppendRelayState encodes the fields of a relay state head (KeyRelayWatch).
func AppendRelayState(noRelay bool) []byte {
	var v uint64
	if noRelay {
		v = 1
	}
	return AppendUint(nil, KeyNoRelay, v)
}

// ParseRelayState decodes the fields of a relay state head.
func ParseRelayState(fields []byte) (noRelay bool, err error) {
	seen := false
	err = Common.Decode(fields, func(f Field) error {
		if f.Key == KeyNoRelay {
			noRelay, seen = f.Value == 1, true
		}
		return nil
	})
	if err == nil && !seen {
		err = ErrMalformed
	}
	return noRelay, err
}
