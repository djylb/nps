package legacyclient

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/netaddr"
)

// Client is an npc of the old protocol: tcp and udp links only. It exists for
// tests of what the server does with such a client.
type Client struct {
	server, carrier, vkey string
	disconnect            int
	uuid                  string
	tunnel                atomic.Pointer[legacy.Carrier]
	signal                atomic.Pointer[conn.Conn]
	ctx                   context.Context
	cancel                context.CancelFunc
	once                  sync.Once
}

// New returns a client of server over carrier with vkey.
func New(server, vkey, carrier string, disconnect int) *Client {
	return &Client{server: server, carrier: carrier, vkey: vkey, disconnect: disconnect}
}

// Start connects and serves links until ctx ends or the connection is lost.
func (s *Client) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.Close()
	nc, uuid, server, err := dialServer(s.carrier, s.vkey, s.server, "", "", nil)
	if err != nil {
		return
	}
	s.uuid = uuid
	if err := SendType(nc, legacy.WorkChan, uuid); err != nil {
		_ = nc.Close()
		return
	}
	tun := legacy.NewTunnel(nc, server.via.Carrier, clientCaps(), s.disconnect, true)
	s.tunnel.Store(&tun)
	go func() {
		defer func() { _ = nc.Close() }()
		for s.ctx.Err() == nil {
			src, err := tun.AcceptStream(s.ctx)
			if err != nil {
				s.Close()
				return
			}
			go s.handle(src)
		}
	}()
	stream, err := tun.OpenStream(s.ctx)
	if err != nil {
		return
	}
	stream = conn.SignalStream(stream)
	sc := conn.NewConn(stream)
	if err := SendType(sc, legacy.WorkMain, uuid); err != nil {
		_ = stream.Close()
		return
	}
	s.signal.Store(sc)
	for s.ctx.Err() == nil {
		// Nothing is sent back: only what ends the connection matters here.
		if _, err := legacy.ReadFlag(sc); err != nil {
			return
		}
		if _, err := legacy.ReadFrame(sc); err != nil {
			return
		}
	}
}

func (s *Client) handle(src net.Conn) {
	lk, err := legacy.ReadLink(src)
	if err != nil || lk == nil {
		_ = src.Close()
		return
	}
	if lk.Option.NeedAck {
		if err := legacy.WriteAck(src, lk.Option.Timeout); err != nil {
			_ = src.Close()
			return
		}
	}
	if lk.ConnType == conn.LinkUDP5 {
		conn.HandleUdp5(s.ctx, src, lk.Option.Timeout, "")
		return
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	target, err := d.DialContext(s.ctx, lk.ConnType, netaddr.FormatAddress(strings.TrimSpace(lk.Host)))
	if err != nil {
		_ = src.Close()
		return
	}
	conn.CopyWaitGroup(src, target, lk.Crypt, lk.Compress, nil, nil, nil, 0, nil, nil, false, lk.ConnType == conn.LinkUDP && clientCaps().FramedUDP)
}

// Close ends the client.
func (s *Client) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if t := s.tunnel.Load(); t != nil {
			_ = (*t).Close()
		}
		if c := s.signal.Load(); c != nil {
			_ = c.Close()
		}
	})
}
