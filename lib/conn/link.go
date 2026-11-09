package conn

import (
	"net"
	"time"
)

// ClientSession is how the server's proxies reach a client: they open links on
// it, whatever protocol carries them.
type ClientSession interface {
	// OpenLink opens a connection to link's target through the client. taskID
	// is the task the link is for, 0 if it is for none.
	OpenLink(link *Link, taskID int) (net.Conn, error)
}

type Link struct {
	ConnType string //连接类型
	Host     string //目标
	// Crypt and Compress are what the opener still applies to the connection
	// OpenLink returned: a protocol that encrypts or compresses a link
	// itself clears them.
	Crypt      bool //加密
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
	// Compress is the compression of a link carried by the core protocol,
	// algorithms in order of preference; the old protocol uses Link.Compress.
	Compress []string `json:"-"`
	// Gone reports that the connection this link is for went away, so that
	// a wait for the client need not last; nil is never.
	Gone func() bool `json:"-"`
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
