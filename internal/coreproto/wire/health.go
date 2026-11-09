package wire

import "errors"

// HealthGroup is one health section of a CONFIG snapshot (§5.3).
type HealthGroup struct {
	Name     string
	Type     string // tcp, http or https
	Path     string
	Interval uint64
	Timeout  uint64
	Fall     uint64
	Targets  []string
}

// HealthReport is one record of a HEALTH stream (§5.3).
type HealthReport struct {
	Name, Addr string
	Up         bool
}

var (
	keyHlName     = key(1, true, true)
	keyHlType     = key(2, true, false)
	keyHlPath     = key(3, false, true)
	keyHlInterval = key(4, true, false)
	keyHlTimeout  = key(5, true, false)
	keyHlFall     = key(6, true, false)
	keyHlTarget   = key(7, true, true)

	keyHrName = key(1, true, true)
	keyHrAddr = key(2, true, true)
	keyHrUp   = key(3, true, false)
)

var (
	healthSchema = newSchema(one(keyHlName), one(keyHlType), one(keyHlPath), one(keyHlInterval),
		one(keyHlTimeout), one(keyHlFall), many(keyHlTarget))
	healthReportSchema = newSchema(one(keyHrName), one(keyHrAddr), one(keyHrUp))
)

var healthTypes = map[string]uint64{"tcp": 1, "http": 2, "https": 3}

// MaxHealthRecord bounds one record of a HEALTH stream.
const MaxHealthRecord = 4 << 10

func appendHealthGroup(b []byte, g *HealthGroup) []byte {
	var e []byte
	e = AppendBytes(e, keyHlName, []byte(g.Name))
	e = AppendUint(e, keyHlType, healthTypes[g.Type])
	if g.Path != "" {
		e = AppendBytes(e, keyHlPath, []byte(g.Path))
	}
	e = AppendUint(e, keyHlInterval, g.Interval)
	e = AppendUint(e, keyHlTimeout, g.Timeout)
	e = AppendUint(e, keyHlFall, g.Fall)
	for _, t := range g.Targets {
		e = AppendBytes(e, keyHlTarget, []byte(t))
	}
	return AppendBytes(b, keyCfgHealth, e)
}

// parseHealthGroup reads an entry; ok is false for one to drop (§5), an
// unknown type or critical field among them.
func parseHealthGroup(b []byte) (g HealthGroup, ok bool, err error) {
	known := true
	err = healthSchema.Decode(b, func(f Field) error {
		switch f.Key {
		case keyHlName:
			g.Name = string(f.Data)
		case keyHlType:
			known = false
			for name, v := range healthTypes {
				if v == f.Value {
					g.Type, known = name, true
				}
			}
		case keyHlPath:
			g.Path = string(f.Data)
		case keyHlInterval:
			g.Interval = f.Value
		case keyHlTimeout:
			g.Timeout = f.Value
		case keyHlFall:
			g.Fall = f.Value
		case keyHlTarget:
			g.Targets = append(g.Targets, string(f.Data))
		}
		return nil
	})
	if errors.Is(err, ErrUnsupported) {
		return g, false, nil
	}
	return g, err == nil && known && g.Name != "" && g.Type != "", err
}

// AppendHealthReport appends one record: Len Field*. up is always written, so
// that a record says what it reports whatever the receiver's default.
func AppendHealthReport(b []byte, r HealthReport) []byte {
	e := AppendBytes(nil, keyHrName, []byte(r.Name))
	e = AppendBytes(e, keyHrAddr, []byte(r.Addr))
	var up uint64
	if r.Up {
		up = 1
	}
	e = AppendUint(e, keyHrUp, up)
	return append(AppendVarint(b, uint64(len(e))), e...)
}

// ParseHealthReport parses the fields of one record.
func ParseHealthReport(b []byte) (HealthReport, error) {
	var r HealthReport
	err := healthReportSchema.Decode(b, func(f Field) error {
		switch f.Key {
		case keyHrName:
			r.Name = string(f.Data)
		case keyHrAddr:
			r.Addr = string(f.Data)
		case keyHrUp:
			r.Up = f.Value == 1
		}
		return nil
	})
	if err == nil && (r.Name == "" || r.Addr == "") {
		err = ErrMalformed
	}
	return r, err
}
