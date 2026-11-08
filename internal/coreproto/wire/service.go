package wire

import "crypto/sha256"

// ServiceIDLen is the length of a virtual service id (§8).
const ServiceIDLen = 16

// ServiceID is the id of the service of the given kind and name (§8).
func ServiceID(kind, name string) []byte {
	h := sha256.New()
	h.Write([]byte("nps vservice"))
	h.Write(AppendVarint(nil, uint64(len(kind))))
	h.Write([]byte(kind))
	h.Write(AppendVarint(nil, uint64(len(name))))
	h.Write([]byte(name))
	return h.Sum(nil)[:ServiceIDLen]
}

// Service is one entry of a SERVICE snapshot (§8).
type Service struct {
	ID         []byte
	Kind, Name string
	Meta       string
}

// Fields of a SERVICE body and of its entries (§8).
var (
	keySvcEntry = key(1, true, true)

	keySvcID   = key(1, true, true)
	keySvcKind = key(2, true, true)
	keySvcName = key(3, true, true)
	keySvcMeta = key(4, false, true)
)

var (
	serviceSchema      = newSchema(many(keySvcEntry))
	serviceEntrySchema = newSchema(one(keySvcID), one(keySvcKind), one(keySvcName), one(keySvcMeta))
)

// AppendServiceOpen is the OPEN_INFO of a SERVICE stream: Op seq.
func AppendServiceOpen(seq uint64) []byte {
	return AppendUint(AppendOpenInfo(nil, OpService), KeySeq, seq)
}

// ParseServiceOpen returns the seq of SERVICE's fields; 0 is malformed.
func ParseServiceOpen(fields []byte) (uint64, error) { return ParseConfigOpen(fields) }

// AppendServices appends a SERVICE body.
func AppendServices(b []byte, list []Service) []byte {
	for _, s := range list {
		e := AppendBytes(nil, keySvcID, s.ID)
		e = AppendBytes(e, keySvcKind, []byte(s.Kind))
		e = AppendBytes(e, keySvcName, []byte(s.Name))
		if s.Meta != "" {
			e = AppendBytes(e, keySvcMeta, []byte(s.Meta))
		}
		b = AppendBytes(b, keySvcEntry, e)
	}
	return b
}

// ParseServices parses a SERVICE body. Every entry must have all of id, kind
// and name, the id being the one they derive.
func ParseServices(b []byte) ([]Service, error) {
	var list []Service
	err := serviceSchema.Decode(b, func(f Field) error {
		var s Service
		var haveKind, haveName bool
		if err := serviceEntrySchema.Decode(f.Data, func(e Field) error {
			switch e.Key {
			case keySvcID:
				s.ID = append([]byte(nil), e.Data...)
			case keySvcKind:
				s.Kind, haveKind = string(e.Data), true
			case keySvcName:
				s.Name, haveName = string(e.Data), true
			case keySvcMeta:
				s.Meta = string(e.Data)
			}
			return nil
		}); err != nil {
			return err
		}
		if !haveKind || !haveName || string(s.ID) != string(ServiceID(s.Kind, s.Name)) {
			return ErrMalformed
		}
		list = append(list, s)
		return nil
	})
	return list, err
}
