package wire

// Op is the operation of a stream (§2.1).
type Op uint64

// Operations.
const (
	OpConfig  Op = 1
	OpTCP     Op = 2
	OpUDP     Op = 3
	OpLink    Op = 4
	OpPipe    Op = 5
	OpPunch   Op = 6
	OpService Op = 7
	OpHealth  Op = 8
	OpUpdate  Op = 9
)

// Known reports whether o is in the table of operations.
func (o Op) Known() bool { return o >= OpConfig && o <= OpUpdate }

// FromServer reports whether the server may open o towards the client: TCP
// and UDP go from the entry to the exit, LINK both ways (§2.1).
func (o Op) FromServer() bool {
	return o == OpConfig || o == OpUpdate || o == OpLink || o == OpTCP || o == OpUDP
}

// FromClient reports whether a client may open o towards the server.
func (o Op) FromClient() bool { return o.Known() && o != OpConfig && o != OpUpdate }

// Common fields (§2.2), shared by Hello, OPEN_INFO and reply heads.
var (
	KeyTarget      = key(1, true, true)
	KeySource      = key(2, false, true)
	KeyWantReply   = key(3, true, false)
	KeyCompress    = key(4, true, false)
	KeyService     = key(5, true, true)
	KeyLinkID      = key(6, true, false)
	KeyCap         = key(7, false, false)
	KeyLabel       = key(8, false, true)
	KeyAgent       = key(9, false, true)
	KeyCode        = key(10, true, false)
	KeyReason      = key(11, false, true)
	KeySeq         = key(12, true, false)
	KeyTunnel      = key(13, true, false)
	KeyAccess      = key(14, true, true)
	KeyLinkKey     = key(16, true, true)
	KeyConnMode    = key(17, true, false)
	KeyScope       = key(18, true, true)
	KeyNameDest    = key(19, true, false)
	KeyCompressAlg = key(20, false, false)
	KeyServerExit  = key(21, true, false)
	KeyBackground  = key(22, false, false)
	KeyServerAddr  = key(23, false, true)
	KeyTargetType  = key(24, true, false)
)

// Common is the table of the common fields.
var Common = newSchema(
	many(KeyTarget), one(KeySource), one(KeyWantReply), one(KeyCompress), one(KeyService),
	one(KeyLinkID), many(KeyCap), one(KeyLabel), one(KeyAgent), one(KeyCode), one(KeyReason),
	one(KeySeq), one(KeyTunnel), one(KeyAccess), one(KeyLinkKey), one(KeyConnMode), one(KeyScope),
	one(KeyNameDest), many(KeyCompressAlg), one(KeyServerExit), one(KeyBackground),
	one(KeyServerAddr), one(KeyTargetType),
)

// Capabilities (§2.3).
const (
	CapLink    uint64 = 1
	CapHealth  uint64 = 2
	CapUpdate  uint64 = 3
	CapShaping uint64 = 4
)

// Compression algorithms (§7.1).
const (
	CompressFlate  uint64 = 1
	CompressZstd   uint64 = 2
	CompressSnappy uint64 = 3
)

// KnownCompress reports whether v is in the table of algorithms.
func KnownCompress(v uint64) bool { return v >= CompressFlate && v <= CompressSnappy }

// Handshake limits (§3.5).
const (
	MaxHelloRecv = 1024 // ClientConfig.MaxAppData, ServerConfig.MaxAppData
	MaxLabel     = 64   // label and agent
	MaxOpenInfo  = 960  // §4.1
)

// Error codes (§10).
const (
	CodeOK                 uint64 = 0
	CodeZmuxRejected       uint64 = 0x5a4d0002
	CodeGeneralFailure     uint64 = 0x5a4d0101
	CodeNotAllowed         uint64 = 0x5a4d0102
	CodeNetworkUnreachable uint64 = 0x5a4d0103
	CodeHostUnreachable    uint64 = 0x5a4d0104
	CodeConnectionRefused  uint64 = 0x5a4d0105
	CodeAddrTypeUnsupp     uint64 = 0x5a4d0108
	CodeDNSFailure         uint64 = 0x5a4d0110
	CodeConnectTimeout     uint64 = 0x5a4d0111
	CodeMalformed          uint64 = 0x4e500001
	CodeUnsupported        uint64 = 0x4e500002
	CodeAuth               uint64 = 0x4e500003
	CodeTunnelOffline      uint64 = 0x4e500004
	CodeQuotaExhausted     uint64 = 0x4e500005
	CodeRateLimited        uint64 = 0x4e500006
	CodeInternal           uint64 = 0x4e500009
	CodeDisabled           uint64 = 0x4e50000b
	CodeReplaced           uint64 = 0x4e50000c
	CodeNotFound           uint64 = 0x4e50000d
	CodeScopeDenied        uint64 = 0x4e50000e
	CodeServiceUnavailable uint64 = 0x4e50000f
	CodeDrain              uint64 = 0x4e500011
	CodeCompressError      uint64 = 0x4e500012
)

// ErrorCode is the stream reset code of a decode error.
func ErrorCode(err error) uint64 {
	if err == ErrUnsupported {
		return CodeUnsupported
	}
	return CodeMalformed
}
