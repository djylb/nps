// Package pmux This module is used for port reuse
// Distinguish client, web manager , HTTP and HTTPS according to the difference of protocol
package pmux

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
)

const (
	HTTPGet     = 716984
	HTTPPost    = 807983
	HTTPHead    = 726965
	HTTPPut     = 808585
	HTTPDelete  = 686976
	HTTPConnect = 677978
	HTTPOptions = 798084
	HTTPTrace   = 848265
	CLIENT      = 848384
	// AcceptTimeout bounds both protocol recognition of a new connection and
	// the wait for the target listener to accept it.
	AcceptTimeout = 10 * time.Second

	maxAcceptBackoff = time.Second
	maxHeaderBytes   = http.DefaultMaxHeaderBytes
)

type listenerKind int

const (
	kindClient listenerKind = iota
	kindClientTls
	kindClientWs
	kindClientWss
	kindHttp
	kindHttps
	kindManager
	kindCount
)

type PortMux struct {
	net.Listener
	port        int
	managerHost string
	clientHost  string

	mu        sync.RWMutex
	listeners [kindCount]*PortListener

	closed    chan struct{}
	closeOnce sync.Once
}

func NewPortMux(port int, managerHost, clientHost string) (*PortMux, error) {
	pMux := newPortMux(port, managerHost, clientHost)
	if err := pMux.Start(); err != nil {
		_ = pMux.Close()
		return nil, err
	}
	return pMux, nil
}

func newPortMux(port int, managerHost, clientHost string) *PortMux {
	return &PortMux{
		managerHost: managerHost,
		clientHost:  clientHost,
		port:        port,
		closed:      make(chan struct{}),
	}
}

func (pMux *PortMux) Start() error {
	// Port multiplexing is based on TCP only
	tcpAddr, err := net.ResolveTCPAddr("tcp", "0.0.0.0:"+strconv.Itoa(pMux.port))
	if err != nil {
		return err
	}
	l, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	pMux.Listener = l
	go pMux.serve(l)
	return nil
}

func (pMux *PortMux) serve(l net.Listener) {
	var delay time.Duration
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				_ = pMux.Close()
				return
			}
			// Transient failures such as EMFILE or ECONNABORTED: back off and retry.
			if delay == 0 {
				delay = 5 * time.Millisecond
			} else {
				delay = min(delay*2, maxAcceptBackoff)
			}
			logs.Warn("port mux accept error: %v; retrying in %v", err, delay)
			t := time.NewTimer(delay)
			select {
			case <-t.C:
			case <-pMux.closed:
				t.Stop()
				return
			}
			continue
		}
		delay = 0
		go pMux.process(conn)
	}
}

// snapshot returns the listeners registered at this moment.
func (pMux *PortMux) snapshot() [kindCount]*PortListener {
	pMux.mu.RLock()
	defer pMux.mu.RUnlock()
	return pMux.listeners
}

func (pMux *PortMux) process(conn net.Conn) {
	if conn == nil {
		return
	}
	target, rs, readMore := pMux.classify(conn)
	if target == nil {
		_ = conn.Close()
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return
	}
	timer := time.NewTimer(AcceptTimeout)
	defer timer.Stop()
	select {
	case target.connCh <- newPortConn(conn, rs, readMore):
		return
	case <-target.done:
	case <-pMux.closed:
	case <-timer.C:
	}
	_ = conn.Close()
}

// classify reads the leading bytes of conn under a read deadline and picks the
// listener it belongs to. A nil listener means the connection must be dropped.
func (pMux *PortMux) classify(conn net.Conn) (*PortListener, []byte, bool) {
	if err := conn.SetReadDeadline(time.Now().Add(AcceptTimeout)); err != nil {
		return nil, nil, false
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, nil, false
	}
	ls := pMux.snapshot()
	switch common.BytesToNum(buf) {
	case HTTPConnect, HTTPDelete, HTTPGet, HTTPHead, HTTPOptions, HTTPPost, HTTPPut, HTTPTrace: //http and manager
		httpL, managerL, wsL := ls[kindHttp], ls[kindManager], ls[kindClientWs]
		n := 0
		for _, l := range []*PortListener{httpL, managerL, wsL} {
			if l != nil {
				n++
			}
		}
		switch {
		case n > 1:
			return pMux.classifyHttp(conn, buf, httpL, managerL, wsL)
		case httpL != nil:
			return httpL, buf, true
		case managerL != nil:
			return managerL, buf, true
		default:
			return wsL, buf, true
		}
	case CLIENT: // client connection
		return ls[kindClient], buf, false
	default: // https or clientTls or clientWss
		https, tlsL := ls[kindHttps], ls[kindClientTls]
		switch {
		case https != nil && tlsL != nil:
			helloInfo, rawData, err := crypt.ReadClientHello(conn, buf)
			var ne net.Error
			if (errors.As(err, &ne) && ne.Timeout()) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, nil, false
			}
			if err == nil && helloInfo != nil && (helloInfo.ServerName == "" || helloInfo.ServerName == pMux.clientHost) {
				return tlsL, rawData, true
			}
			return https, rawData, true
		case https != nil:
			return https, buf, true
		case tlsL != nil:
			return tlsL, buf, true
		default:
			return ls[kindClientWss], buf, true
		}
	}
}

// classifyHttp reads request headers until the Host header and routes by host.
func (pMux *PortMux) classifyHttp(conn net.Conn, prefix []byte, httpL, managerL, wsL *PortListener) (*PortListener, []byte, bool) {
	var buffer bytes.Buffer
	buffer.Write(prefix)
	r := bufio.NewReader(conn)
	var line []byte
	for {
		b, isPrefix, err := r.ReadLine()
		if err != nil {
			logs.Trace("port mux read http header error: %v", err)
			return nil, nil, false
		}
		buffer.Write(b)
		if buffer.Len() > maxHeaderBytes {
			logs.Trace("port mux http header too large from %v", conn.RemoteAddr())
			return nil, nil, false
		}
		line = append(line, b...)
		if isPrefix {
			continue
		}
		buffer.WriteString("\r\n")
		var target *PortListener
		host, ok := parseHostHeader(line)
		switch {
		case ok:
			// Determine whether it is the same as the manager domain name
			switch ip := common.GetIpByAddr(host); {
			case ip == pMux.managerHost && managerL != nil:
				target = managerL
			case ip == pMux.clientHost && wsL != nil:
				target = wsL
			default:
				target = httpL
			}
		case len(line) == 0:
			// End of headers without a Host header.
			target = httpL
		default:
			line = line[:0]
			continue
		}
		rest, _ := r.Peek(r.Buffered())
		buffer.Write(rest)
		return target, buffer.Bytes(), false
	}
}

// Close stops the underlying listener and wakes up all port listeners.
func (pMux *PortMux) Close() error {
	err := net.ErrClosed
	pMux.closeOnce.Do(func() {
		err = nil
		if pMux.Listener != nil {
			err = pMux.Listener.Close()
		}
		close(pMux.closed)
	})
	return err
}

func parseHostHeader(line []byte) (string, bool) {
	const header = "host:"
	if len(line) < len(header) || !bytes.EqualFold(line[:len(header)], []byte(header)) {
		return "", false
	}
	return string(bytes.TrimSpace(line[len(header):])), true
}

// register installs a new listener for kind, closing any listener it replaces.
// A closed listener keeps its slot, so routing does not shift to another
// protocol; connections routed to it are dropped until it is replaced.
func (pMux *PortMux) register(kind listenerKind) net.Listener {
	var addr net.Addr
	if pMux.Listener != nil {
		addr = pMux.Listener.Addr()
	}
	l := newPortListener(addr, pMux.closed)
	pMux.mu.Lock()
	old := pMux.listeners[kind]
	pMux.listeners[kind] = l
	pMux.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return l
}

func (pMux *PortMux) GetClientListener() net.Listener {
	return pMux.register(kindClient)
}

func (pMux *PortMux) GetClientTlsListener() net.Listener {
	return pMux.register(kindClientTls)
}

func (pMux *PortMux) GetClientWsListener() net.Listener {
	return pMux.register(kindClientWs)
}

func (pMux *PortMux) GetClientWssListener() net.Listener {
	return pMux.register(kindClientWss)
}

func (pMux *PortMux) GetHttpListener() net.Listener {
	return pMux.register(kindHttp)
}

func (pMux *PortMux) GetHttpsListener() net.Listener {
	return pMux.register(kindHttps)
}

func (pMux *PortMux) GetManagerListener() net.Listener {
	return pMux.register(kindManager)
}
