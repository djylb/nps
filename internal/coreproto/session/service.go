package session

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

const (
	// maxServiceBody bounds a SERVICE snapshot, as CONFIG's is (§4.3).
	maxServiceBody = maxConfigBody
	// serviceTimeout bounds sending or reading one snapshot.
	serviceTimeout = 30 * time.Second
)

// serveServices takes a SERVICE snapshot of the session (§8). It only ever
// affects this session's entries.
func (s *serverSession) serveServices(lc *linkConn, fields []byte) {
	defer func() { _ = lc.Close() }()
	seq, err := wire.ParseServiceOpen(fields)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	_ = lc.SetReadDeadline(time.Now().Add(serviceTimeout))
	body, err := io.ReadAll(io.LimitReader(lc, maxServiceBody+1))
	if err != nil {
		return
	}
	if len(body) > maxServiceBody {
		lc.abort(wire.CodeMalformed)
		return
	}
	list, err := wire.ParseServices(body)
	if err != nil {
		lc.abort(wire.ErrorCode(err))
		return
	}
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	if seq <= s.serviceSeq {
		return
	}
	s.serviceSeq = seq
	h := s.a.hooks.Load()
	if h == nil || h.Services == nil {
		return
	}
	out := make([]coreapi.ServiceInfo, len(list))
	for i, e := range list {
		out[i] = coreapi.ServiceInfo{Kind: e.Kind, Name: e.Name, Meta: e.Meta, Network: networkName(e.Network)}
	}
	h.Services(s, out)
}

var _ coreapi.ServiceHost = (*clientSession)(nil)

// PublishServices implements coreapi.ServiceHost.
func (s *clientSession) PublishServices(ctx context.Context, list []coreapi.LocalService) error {
	entries := make([]wire.Service, len(list))
	byID := make(map[string]coreapi.LocalService, len(list))
	for i, l := range list {
		id := wire.ServiceID(l.Kind, l.Name)
		entries[i] = wire.Service{ID: id, Kind: l.Kind, Name: l.Name, Meta: l.Meta, Network: networkID(l.Network)}
		byID[string(id)] = l
	}
	// In place before the server can learn of them.
	s.mu.Lock()
	s.services = byID
	s.serviceSeq++
	seq := s.serviceSeq
	s.mu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, serviceTimeout)
	defer cancel()
	st, err := s.zc.OpenStreamSync(sctx)
	if err != nil {
		return err
	}
	lc := &linkConn{Stream: st}
	dl, _ := sctx.Deadline()
	_ = st.SetWriteDeadline(dl)
	if err := st.SetPreamble(wire.AppendServiceOpen(seq)); err != nil {
		lc.abort(wire.CodeInternal)
		return err
	}
	body := wire.AppendServices(nil, entries)
	if len(body) == 0 {
		if err := st.Flush(); err != nil {
			lc.abort(wire.CodeInternal)
			return err
		}
	} else if _, err := st.Write(body); err != nil {
		lc.abort(wire.CodeInternal)
		return err
	}
	return lc.Close()
}

// serveService hands a stream to the local service it names (§8).
func (s *clientSession) serveService(ctx context.Context, lc *linkConn, o *wire.StreamOpen) {
	s.mu.Lock()
	svc, ok := s.services[string(o.Service)]
	s.mu.Unlock()
	if !ok || !networkMatches(svc.Network, o.Op) {
		failLink(lc, o.WantReply, wire.CodeServiceUnavailable)
		return
	}
	if !ackLink(lc, o) {
		return
	}
	var c net.Conn
	c, err := wrapLink(lc, *o, s.zc, s.d.limits)
	if err != nil {
		return
	}
	if o.Op == wire.OpUDP {
		c = netx.NewFramedConn(c, netx.WithFramedDatagrams())
	}
	if o.Source != nil {
		c = sourceConn{Conn: c, remote: o.Source}
	}
	svc.Serve(ctx, c)
}

// sourceConn shows a service the visitor's address.
type sourceConn struct {
	net.Conn
	remote *wire.Address
}

func (c sourceConn) RemoteAddr() net.Addr { return wireAddr{c.remote} }

type wireAddr struct{ a *wire.Address }

func (wireAddr) Network() string  { return "tcp" }
func (w wireAddr) String() string { return w.a.String() }

func networkName(n uint64) string {
	switch n {
	case wire.ServiceUDP:
		return coreapi.NetworkUDP
	case wire.ServiceAll:
		return coreapi.NetworkAll
	}
	return coreapi.NetworkTCP
}

func networkID(n string) uint64 {
	switch n {
	case coreapi.NetworkUDP:
		return wire.ServiceUDP
	case coreapi.NetworkAll:
		return wire.ServiceAll
	}
	return wire.ServiceTCP
}

func networkMatches(n string, op wire.Op) bool {
	switch n {
	case coreapi.NetworkAll:
		return true
	case coreapi.NetworkUDP:
		return op == wire.OpUDP
	}
	return op == wire.OpTCP
}
