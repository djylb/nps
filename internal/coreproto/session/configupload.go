package session

import (
	"context"
	"net"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

// The config upload (wire.OpConfigUpload) and npc register (wire.OpRegister)
// are a 0.35 transition: the byte exchanges of the old protocol's WorkConfig
// and WorkRegister run on a stream of the session, so that npc.conf and these
// commands keep working when only the binary is upgraded. They are removed,
// with this file, in the next major version.

// configUploadReplyTimeout bounds the wait for the admission of a stream.
const configUploadReplyTimeout = 10 * time.Second

func (a *Acceptor) hookConfigUpload() func(coreapi.Session) func(net.Conn) {
	if h := a.hooks.Load(); h != nil {
		return h.ConfigUpload
	}
	return nil
}

func (a *Acceptor) hookRegister() func(coreapi.Session) func(net.Conn) {
	if h := a.hooks.Load(); h != nil {
		return h.Register
	}
	return nil
}

// serveTransition answers such a stream: the bridge admits it through admit,
// then the reply head and the bridge's exchange follow on the stream.
func (s *serverSession) serveTransition(lc *linkConn, admit func(coreapi.Session) func(net.Conn)) {
	if admit == nil {
		failLink(lc, true, wire.CodeUnsupported)
		return
	}
	serve := admit(s)
	if serve == nil {
		failLink(lc, true, wire.CodeNotAllowed)
		return
	}
	_ = lc.SetWriteDeadline(time.Now().Add(configUploadReplyTimeout))
	_, err := lc.Write(wire.AppendReply(nil, wire.CodeOK, "", nil))
	_ = lc.SetWriteDeadline(time.Time{})
	if err != nil {
		lc.abort(wire.CodeInternal)
		return
	}
	serve(lc)
}

var _ coreapi.ConfigUploader = (*clientSession)(nil)

// OpenConfigUpload implements coreapi.ConfigUploader.
func (s *clientSession) OpenConfigUpload(ctx context.Context) (net.Conn, error) {
	return s.openTransition(ctx, wire.OpConfigUpload)
}

// OpenRegister implements coreapi.ConfigUploader.
func (s *clientSession) OpenRegister(ctx context.Context) (net.Conn, error) {
	return s.openTransition(ctx, wire.OpRegister)
}

func (s *clientSession) openTransition(ctx context.Context, op wire.Op) (net.Conn, error) {
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	lc := &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
	if err := st.SetPreamble(wire.AppendOpenInfo(nil, op)); err != nil {
		lc.abort(wire.CodeInternal)
		return nil, err
	}
	if err := st.Flush(); err != nil {
		lc.abort(wire.CodeInternal)
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, configUploadReplyTimeout)
	defer cancel()
	if err := readLinkReply(rctx, lc); err != nil {
		lc.abort(wire.CodeInternal)
		return nil, err
	}
	return lc, nil
}
