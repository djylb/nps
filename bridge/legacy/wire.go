package legacy

import (
	"io"
	"net"

	"github.com/djylb/nps/internal/tunnelwire"
)

// Caps are what a peer speaks; every version decision reads one of these.
type Caps = tunnelwire.Capabilities

// CapsOf returns what a peer announcing protocol index and client version speaks.
func CapsOf(index int, clientVersion string) Caps { return tunnelwire.Caps(index, clientVersion) }

// The protocol's version table.
const (
	LatestIndex            = tunnelwire.LatestIndex
	P2PMinVersion          = tunnelwire.P2PMinVersion
	RemoteUpdateMinVersion = tunnelwire.RemoteUpdateMinVersion
)

// CoreVersion returns the version string of a protocol index.
func CoreVersion(index int) string { return tunnelwire.Version(index) }

// MinCoreVersion returns the oldest protocol a server accepts.
func MinCoreVersion(secure bool) string { return tunnelwire.MinVersion(secure) }

// Flags of the protocol.
const (
	Magic            = tunnelwire.Magic
	WorkMain         = tunnelwire.WorkMain
	WorkChan         = tunnelwire.WorkChan
	WorkVisitor      = tunnelwire.WorkVisitor
	WorkConfig       = tunnelwire.WorkConfig
	WorkRegister     = tunnelwire.WorkRegister
	WorkSecret       = tunnelwire.WorkSecret
	WorkSecretAttach = tunnelwire.WorkSecretAttach
	WorkSecretLink   = tunnelwire.WorkSecretLink
	WorkFile         = tunnelwire.WorkFile
	WorkP2P          = tunnelwire.WorkP2P
	WorkP2PSession   = tunnelwire.WorkP2PSession
	WorkStatus       = tunnelwire.WorkStatus
	ResClose         = tunnelwire.ResClose
	P2PPunchStart    = tunnelwire.P2PPunchStart
	P2PRevokeGrant   = tunnelwire.P2PRevokeGrant
	UpdateRequest    = tunnelwire.UpdateRequest
	HealthSet        = tunnelwire.HealthSet
	HealthDel        = tunnelwire.HealthDel
	FlagNewTask      = tunnelwire.FlagNewTask
	FlagNewConf      = tunnelwire.FlagNewConf
	FlagNewHost      = tunnelwire.FlagNewHost
)

// Handshake errors.
var (
	ErrProbe           = tunnelwire.ErrProbe
	ErrVersion         = tunnelwire.ErrVersion
	ErrUnauthorized    = tunnelwire.ErrUnauthorized
	ErrDisabled        = tunnelwire.ErrDisabled
	ErrVersionMismatch = tunnelwire.ErrVersionMismatch
)

// ServerAuth is what ServerHandshake needs from the bridge.
type ServerAuth struct {
	Secure bool
	// LookupDigest finds the client whose key has the blake2b digest; LookupMD5
	// the one whose key has the MD5 digest, as older clients send it.
	LookupDigest func(digest string) (Credential, error)
	LookupMD5    func(digest string, remote net.Addr) (Credential, error)
	CheckTime    func(ts int64) error
	CheckReplay  func(ts int64, mac []byte) error
	Fingerprint  func() []byte
	NewUUID      func() string
}

// Credential is a client that a key digest names.
type Credential = tunnelwire.Credential

// ServerResult describes a verified client.
type ServerResult = tunnelwire.ServerResult

// ServerHandshake verifies the client on rw against the clients auth finds.
func ServerHandshake(rw io.ReadWriter, remote net.Addr, auth ServerAuth) (*ServerResult, error) {
	return tunnelwire.ServerHandshake(rw, tunnelwire.ServerConfig{
		Secure:       auth.Secure,
		LookupDigest: auth.LookupDigest,
		LookupMD5:    func(digest string) (Credential, error) { return auth.LookupMD5(digest, remote) },
		CheckTime:    auth.CheckTime,
		CheckReplay:  auth.CheckReplay,
		Fingerprint:  auth.Fingerprint,
		NewUUID:      auth.NewUUID,
	})
}

// ClientConfig is what ClientHandshake sends and how it checks the server.
type ClientConfig = tunnelwire.ClientConfig

// ClientResult is what a verified server told.
type ClientResult = tunnelwire.ClientResult

// ClientHandshake verifies rw's server and proves the key to it.
func ClientHandshake(rw io.ReadWriter, cfg ClientConfig) (*ClientResult, error) {
	return tunnelwire.ClientHandshake(rw, cfg)
}

// WriteWork asks for work flag on the connection w of a client with caps.
func WriteWork(w io.Writer, caps Caps, flag, uuid string) error {
	return tunnelwire.WriteWork(w, caps, flag, uuid)
}

// ReadWork reads what work a client with caps at addr asks for, and the UUID of
// its node: its own, or one named after addr.
func ReadWork(r io.Reader, caps Caps, addr string) (flag, uuid string, err error) {
	flag, uuid, err = tunnelwire.ReadWork(r, caps)
	if err != nil {
		return flag, uuid, err
	}
	if uuid == "" {
		uuid = tunnelwire.FallbackNodeID(caps, addr)
	}
	return flag, uuid, nil
}

// ReadFlag reads a flag.
func ReadFlag(r io.Reader) (string, error) { return tunnelwire.ReadFlag(r) }

// ReadExact reads n bytes.
func ReadExact(r io.Reader, n int) ([]byte, error) { return tunnelwire.ReadExact(r, n) }

// ReadFrame reads a length-prefixed frame.
func ReadFrame(r io.Reader) ([]byte, error) { return tunnelwire.ReadFrame(r) }

// KeyDigest is the digest of a client key on the wire.
func KeyDigest(key string) string { return tunnelwire.KeyDigest(key) }
