package legacy

import (
	"io"

	"github.com/djylb/nps/internal/tunnelwire/control"
)

// The P2P messages npc and the bridge exchange on a control stream before it
// turns into a TLS stream for the hole punching library, and the names both
// sides agree on.
//
// A visitor opens a stream with WorkP2P and sends a P2PRequest; the bridge
// sends the provider a P2PInvite (P2PPunchStart) on its signal connection, the
// provider opens a stream with WorkP2PSession carrying a P2PJoin, and the
// bridge answers both with a P2PReply once the session exists. After an
// accepting reply, npc runs a TLS client handshake that pins the bridge
// certificate and the bridge a TLS server handshake with it; the TLS stream
// then carries the punching session.
type (
	P2PRequest      = control.P2PRequest
	P2PInvite       = control.P2PInvite
	P2PJoin         = control.P2PJoin
	P2PReply        = control.P2PReply
	P2PRefusedError = control.P2PRefusedError
	P2PRevoke       = control.P2PRevoke
	SecretChallenge = control.SecretChallenge
	SecretAttach    = control.SecretAttach
	SecretGranted   = control.SecretGranted
	SecretOpen      = control.SecretOpen
)

const (
	P2PTransportMode   = control.P2PTransportMode
	P2PCodeRefused     = control.P2PCodeRefused
	P2PCodeUnsupported = control.P2PCodeUnsupported
	P2PCodeOffline     = control.P2PCodeOffline
	P2PCodeBusy        = control.P2PCodeBusy
)

// ErrP2PMessageSize is a message whose length prefix is out of range.
var ErrP2PMessageSize = control.ErrP2PMessageSize

// WriteP2P sends v as one message.
func WriteP2P(w io.Writer, v any) error { return control.WriteP2P(w, v) }

// ReadP2P reads one message into v.
func ReadP2P(r io.Reader, v any) error { return control.ReadP2P(r, v) }
