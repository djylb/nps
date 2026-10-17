// Package p2pproto holds the messages npc and the bridge exchange on a P2P
// control stream before it turns into a TLS stream for the hole punching
// library, and the names both sides agree on.
//
// A visitor opens a stream with WORK_P2P and sends a Request; the bridge
// sends the provider an Invite (P2P_PUNCH_START) on its signal connection,
// the provider opens a stream with WORK_P2P_SESSION carrying a Join, and the
// bridge answers both with a Reply once the session exists. After an
// accepting Reply,
// npc runs a TLS client handshake that pins the bridge certificate and the
// bridge a TLS server handshake with it; the TLS stream then carries the
// punching session.
package p2pproto

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/djylb/nps/lib/conn"
)

const (
	// TransportMode is the upper-layer transport both sides offer.
	TransportMode = "quic"
	// maxMessage bounds a message on the wire.
	maxMessage = 4 << 10
)

// Request opens a session as the visitor.
type Request struct {
	Password   string `json:"password"`             // MD5 of the task password
	Background bool   `json:"background,omitempty"` // the visitor relays meanwhile; nobody waits for the session
	ServerIP   string `json:"server_ip,omitempty"`  // the server address the client's bridge connection reached, when known
}

// Invite asks the provider on its signal connection to join a session.
type Invite struct {
	SessionID string `json:"session_id"` // the invitation id
	Token     string `json:"token"`      // one-time join token
}

// Join answers an invitation.
type Join struct {
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	ServerIP  string `json:"server_ip,omitempty"` // as in Request
	Decline   string `json:"decline,omitempty"`   // the provider refuses the session, and why
}

// Reply codes. An empty code accepts the request.
const (
	CodeRefused     = "refused"     // unknown, disabled or misconfigured task, P2P off on the server
	CodeUnsupported = "unsupported" // the peer's npc is too old
	CodeOffline     = "offline"     // the provider is not connected
	CodeBusy        = "busy"        // the server's session limits refused the session
)

// Reply answers a Request or a Join.
type Reply struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// RefusedError is a Reply that did not accept the request.
type RefusedError struct {
	Code    string
	Message string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("p2p refused by server (%s): %s", e.Code, e.Message)
}

// Err returns the reply as an error: nil when it accepts, a *RefusedError
// otherwise.
func (r Reply) Err() error {
	if r.Code == "" {
		return nil
	}
	return &RefusedError{Code: r.Code, Message: r.Message}
}

// ErrMessageSize is a message whose length prefix is out of range.
var ErrMessageSize = errors.New("p2p message length out of range")

// Write sends v as one length-prefixed JSON message.
func Write(c *conn.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxMessage {
		return errors.New("p2p message too large")
	}
	if err := c.WriteLenContent(b); err != nil {
		return err
	}
	return c.FlushBuf()
}

// Read reads one message written by Write into v.
func Read(c *conn.Conn, v any) error {
	n, err := c.GetLen()
	if err != nil {
		return err
	}
	if n <= 0 || n > maxMessage {
		return ErrMessageSize
	}
	b, err := c.GetShortContent(n)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
