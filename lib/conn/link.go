package conn

import (
	"io"
	"net"
	"time"
)

// Secret is a connection that asked for a secret task by its password.
type Secret struct {
	Password string
	Conn     *Conn
	Wire     SecretWire
}

// SecretWire is what a secret connection's protocol does, as the proxy serving
// it needs it.
type SecretWire struct {
	// ReadLink reads the Link the connection may start with.
	ReadLink func(io.Reader) (*Link, error)
	// WriteAck answers a Link that asks for an acknowledgement.
	WriteAck func(net.Conn, time.Duration) error
}

func NewSecret(p string, conn *Conn, wire SecretWire) *Secret {
	return &Secret{Password: p, Conn: conn, Wire: wire}
}

// ClientSession is how the server's proxies reach a client: they open links on
// it, whatever protocol carries them.
type ClientSession interface {
	// OpenLink opens a connection to link's target through the client. taskID
	// is the task the link is for, 0 if it is for none.
	OpenLink(link *Link, taskID int) (net.Conn, error)
}

type Link struct {
	ConnType   string //连接类型
	Host       string //目标
	Crypt      bool   //加密
	Compress   bool
	LocalProxy bool
	RemoteAddr string
	Option     Options
}

type Option func(*Options)

type Options struct {
	Timeout time.Duration
	NeedAck bool
	// Node is the UUID of the node to open the link through, chosen by the
	// server; empty leaves the choice to the client's select mode.
	Node string `json:"-"`
}

var defaultTimeOut = time.Second * 5

func NewLink(connType string, host string, crypt bool, compress bool, remoteAddr string, localProxy bool, opts ...Option) *Link {
	options := newOptions(opts...)

	return &Link{
		RemoteAddr: remoteAddr,
		ConnType:   connType,
		Host:       host,
		Crypt:      crypt,
		Compress:   compress,
		LocalProxy: localProxy,
		Option:     options,
	}
}

func newOptions(opts ...Option) Options {
	opt := Options{
		Timeout: defaultTimeOut,
		NeedAck: false,
	}
	for _, o := range opts {
		o(&opt)
	}
	return opt
}
