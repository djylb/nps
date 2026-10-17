package pmux

import (
	"net"
	"sync"
)

// PortListener receives the connections a PortMux routes to one protocol.
type PortListener struct {
	connCh    chan *PortConn
	done      chan struct{}
	muxClosed <-chan struct{}
	closeOnce sync.Once
	addr      net.Addr
}

func newPortListener(addr net.Addr, muxClosed <-chan struct{}) *PortListener {
	return &PortListener{
		connCh:    make(chan *PortConn),
		done:      make(chan struct{}),
		muxClosed: muxClosed,
		addr:      addr,
	}
}

// Accept returns net.ErrClosed once the listener or its PortMux is closed.
func (pListener *PortListener) Accept() (net.Conn, error) {
	select {
	case <-pListener.done:
		return nil, net.ErrClosed
	case <-pListener.muxClosed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case conn := <-pListener.connCh:
		return conn, nil
	case <-pListener.done:
		return nil, net.ErrClosed
	case <-pListener.muxClosed:
		return nil, net.ErrClosed
	}
}

func (pListener *PortListener) Close() error {
	err := net.ErrClosed
	pListener.closeOnce.Do(func() {
		err = nil
		close(pListener.done)
	})
	return err
}

func (pListener *PortListener) Addr() net.Addr {
	return pListener.addr
}
