// Package control holds the messages of the protocol's control streams. Their
// JSON field names and the frames around them are frozen for released peers:
// fields are only ever ignored or added, never renamed.
package control

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/djylb/nps/internal/tunnelwire"
)

// Link asks a client for a connection to a target.
type Link struct {
	ConnType   string
	Host       string
	Crypt      bool
	Compress   bool
	LocalProxy bool
	RemoteAddr string
	Option     LinkOptions
}

// LinkOptions are the options a Link carries.
type LinkOptions struct {
	Timeout time.Duration
	NeedAck bool
}

// Config is the client's connection settings.
type Config struct {
	U        string
	P        string
	Compress bool
	Crypt    bool
}

// Flow is a traffic account.
type Flow struct {
	ExportFlow int64
	InletFlow  int64
	FlowLimit  int64
	TimeLimit  time.Time
}

// Rate is a rate limiter's state, in bytes per second.
type Rate struct {
	NowRate int64 `json:"NowRate"`
	Limit   int64 `json:"Limit"`
}

// SourceLists are access lists of visitor addresses.
type SourceLists struct {
	IpListPriority int
	BlackIpList    []string
	WhiteIpList    []string
	RegisteredOnly bool
}

// RateLimits are an entry's limits.
type RateLimits struct {
	UploadLimit   int
	DownloadLimit int
}

// Target is where an entry forwards to.
type Target struct {
	TargetStr     string
	LocalProxy    bool
	ProxyProtocol int
}

// MultiAccount holds the accounts of an entry.
type MultiAccount struct {
	Content    string
	AccountMap map[string]string
}

// Health is what a released client sends of a health check it configured
// itself; the server ignores it.
type Health struct {
	HealthCheckTimeout  int
	HealthMaxFail       int
	HealthCheckInterval int
	HttpHealthUrl       string
	HealthCheckType     string
	HealthCheckTarget   string
}

// Client describes a client sent with FlagNewConf.
type Client struct {
	Cnf             *Config
	Id              int
	VerifyKey       string
	Mode            string
	Addr            string
	LocalAddr       string
	Remark          string
	Status          bool
	IsConnect       bool
	RateLimit       int
	Flow            *Flow
	ExportFlow      int64
	InletFlow       int64
	Rate            *Rate
	NoStore         bool
	NoDisplay       bool
	MaxConn         int
	NowConn         int32
	WebUserName     string
	WebPassword     string
	WebTotpSecret   string
	ConfigConnAllow bool
	MaxTunnelNum    int
	Version         string
	SourceLists
	CreateTime        string
	LastOnlineTime    string
	ConnectionHistory json.RawMessage `json:",omitempty"`
	RegisterKey       string
	RegisterMaxHours  int
}

// Task describes a task sent with FlagNewTask.
type Task struct {
	RateLimits
	MaxConn      int
	Id           int
	Port         int
	ServerIp     string
	Mode         string
	Status       bool
	RunStatus    bool
	Client       *Client
	Ports        string
	Flow         *Flow
	NowConn      int32
	Password     string
	Remark       string
	TargetAddr   string
	TargetType   string
	DestAclMode  int
	DestAclRules string
	SourceLists
	NoStore      bool
	IsHttp       bool
	HttpProxy    bool
	Socks5Proxy  bool
	LocalPath    string
	StripPre     string
	ReadOnly     bool
	Target       *Target
	UserAuth     *MultiAccount
	MultiAccount *MultiAccount
	Health
}

// Host describes a domain entry sent with FlagNewHost.
type Host struct {
	RateLimits
	Id               int
	Host             string
	HeaderChange     string
	RespHeaderChange string
	HostChange       string
	Location         string
	PathRewrite      string
	Remark           string
	Scheme           string
	RedirectURL      string
	HttpsJustProxy   bool
	TlsOffload       bool
	AutoSSL          bool
	CertType         string
	CertHash         string
	CertFile         string
	KeyFile          string
	NoStore          bool
	IsClose          bool
	AutoHttps        bool
	AutoCORS         bool
	CompatMode       bool
	Flow             *Flow
	MaxConn          int
	NowConn          int32
	Client           *Client
	TargetIsHttps    bool
	Target           *Target
	UserAuth         *MultiAccount
	MultiAccount     *MultiAccount
	SourceLists
}

// ErrEmptyInfo is a message holding JSON null.
var ErrEmptyInfo = errors.New("empty info payload")

// Encode returns v as one message: flag, which may be empty, and a frame
// holding its JSON.
func Encode(v any, flag string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return tunnelwire.AppendFrame(append(make([]byte, 0, len(flag)+4+len(b)), flag...), b), nil
}

// Write writes v as one message in one write.
func Write(w io.Writer, v any, flag string) error {
	b, err := Encode(v, flag)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// Read reads a message into v, a pointer to a pointer. It decodes leniently:
// a field of the wrong type is left out rather than failing the message, for
// peers that sent other types over the versions.
func Read(r io.Reader, v any) error {
	b, err := tunnelwire.ReadInfoFrame(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return err
		}
	}
	return nil
}

// WriteStatus reports whether an entry was added.
func WriteStatus(w io.Writer, ok bool) error {
	return binary.Write(w, binary.LittleEndian, ok)
}

// ReadStatus reads what WriteStatus wrote.
func ReadStatus(r io.Reader) (ok bool, err error) {
	err = binary.Read(r, binary.LittleEndian, &ok)
	return ok, err
}

// FileKey names a client's file server: the same on both sides of a file task.
func FileKey(key, mode, serverIP string, port int, localPath, stripPre string, readOnly bool, accounts string) string {
	return tunnelwire.NameUUID(key, mode, serverIP, strconv.Itoa(port), localPath, stripPre, strconv.FormatBool(readOnly), accounts)
}

// P2PTransportMode is the upper-layer transport both sides of a P2P session offer.
const P2PTransportMode = "quic"

// maxP2PMessage bounds a P2P message on the wire.
const maxP2PMessage = 4 << 10

// P2PRequest opens a session as the visitor.
type P2PRequest struct {
	Password   string `json:"password"`             // MD5 of the task password
	Background bool   `json:"background,omitempty"` // the visitor relays meanwhile; nobody waits for the session
	ServerIP   string `json:"server_ip,omitempty"`  // the server address the client's connection reached, when known
}

// P2PInvite asks the provider on its signal connection to join a session.
type P2PInvite struct {
	SessionID string `json:"session_id"` // the invitation id
	Token     string `json:"token"`      // one-time join token
}

// P2PJoin answers an invitation.
type P2PJoin struct {
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	ServerIP  string `json:"server_ip,omitempty"` // as in P2PRequest
	Decline   string `json:"decline,omitempty"`   // the provider refuses the session, and why
}

// P2P reply codes. An empty code accepts the request.
const (
	P2PCodeRefused     = "refused"     // unknown, disabled or misconfigured task, P2P off on the server
	P2PCodeUnsupported = "unsupported" // the peer is too old
	P2PCodeOffline     = "offline"     // the provider is not connected
	P2PCodeBusy        = "busy"        // the server's session limits refused the session
)

// P2PReply answers a P2PRequest or a P2PJoin.
type P2PReply struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// P2PRefusedError is a P2PReply that did not accept the request.
type P2PRefusedError struct {
	Code    string
	Message string
}

func (e *P2PRefusedError) Error() string {
	return fmt.Sprintf("p2p refused by server (%s): %s", e.Code, e.Message)
}

// Err returns the reply as an error: nil when it accepts, a *P2PRefusedError
// otherwise.
func (r P2PReply) Err() error {
	if r.Code == "" {
		return nil
	}
	return &P2PRefusedError{Code: r.Code, Message: r.Message}
}

// ErrP2PMessageSize is a message whose length prefix is out of range.
var ErrP2PMessageSize = errors.New("p2p message length out of range")

// WriteP2P sends v as one length-prefixed JSON message.
func WriteP2P(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxP2PMessage {
		return errors.New("p2p message too large")
	}
	return tunnelwire.WriteFrame(w, b)
}

// ReadP2P reads one message written by WriteP2P into v.
func ReadP2P(r io.Reader, v any) error {
	n, err := tunnelwire.ReadLen(r)
	if err != nil {
		return err
	}
	if n <= 0 || n > maxP2PMessage {
		return ErrP2PMessageSize
	}
	b, err := tunnelwire.ReadExact(r, n)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
