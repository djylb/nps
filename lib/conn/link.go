package conn

import (
	"time"
)

type Secret struct {
	//Type     string // tcp/udp
	Password string
	Conn     *Conn
	//Tunnel   *mux.Mux
}

func NewSecret(p string, conn *Conn) *Secret {
	return &Secret{
		//Type:     tp,
		Password: p,
		Conn:     conn,
		//Tunnel:   tunnel,
	}
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

func LinkTimeout(t time.Duration) Option {
	return func(opt *Options) {
		opt.Timeout = t
	}
}

func WithAck(enabled bool) Option {
	return func(opt *Options) {
		opt.NeedAck = enabled
	}
}
