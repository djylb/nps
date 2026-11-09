package wire

// LinkUsage is one entry of a P2P_USAGE body (§9.6): the bytes of the direct
// pipes of a link, cumulative, from the provider's side.
type LinkUsage struct {
	LinkID  uint64
	In, Out uint64 // received and sent by the provider
}

// MaxUsageBody bounds a P2P_USAGE body; an entry is at most 30 bytes.
const MaxUsageBody = 1 << 20

var (
	keyUsage = key(1, true, true)

	keyUsLinkID = key(1, true, false)
	keyUsIn     = key(2, true, false)
	keyUsOut    = key(3, true, false)
)

var (
	usageSchema      = newSchema(many(keyUsage))
	usageEntrySchema = newSchema(one(keyUsLinkID), one(keyUsIn), one(keyUsOut))
)

// AppendUsageOpen is the OPEN_INFO of a P2P_USAGE stream: the Op alone.
func AppendUsageOpen() []byte { return AppendOpenInfo(nil, OpP2PUsage) }

// AppendUsage appends a P2P_USAGE body.
func AppendUsage(b []byte, list []LinkUsage) []byte {
	for _, u := range list {
		e := AppendUint(nil, keyUsLinkID, u.LinkID)
		e = AppendUint(e, keyUsIn, u.In)
		e = AppendUint(e, keyUsOut, u.Out)
		b = AppendBytes(b, keyUsage, e)
	}
	return b
}

// ParseUsage parses a P2P_USAGE body. An entry needs a non-zero link_id.
func ParseUsage(b []byte) ([]LinkUsage, error) {
	var list []LinkUsage
	err := usageSchema.Decode(b, func(f Field) error {
		var u LinkUsage
		if err := usageEntrySchema.Decode(f.Data, func(e Field) error {
			switch e.Key {
			case keyUsLinkID:
				u.LinkID = e.Value
			case keyUsIn:
				u.In = e.Value
			case keyUsOut:
				u.Out = e.Value
			}
			return nil
		}); err != nil {
			return err
		}
		if u.LinkID == 0 {
			return ErrMalformed
		}
		list = append(list, u)
		return nil
	})
	return list, err
}
