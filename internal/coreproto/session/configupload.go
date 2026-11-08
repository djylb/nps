package session

import (
	"context"
	"net"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/wire"
)

// The config upload (wire.OpConfigUpload) is a 0.35 transition: the byte
// exchange of the old protocol's config upload runs on a stream of the
// session, so that npc.conf keeps working when only the binary is upgraded.
// It is removed, with this file, in the next major version.

// configUploadReplyTimeout bounds the wait for the admission of an upload.
const configUploadReplyTimeout = 10 * time.Second

// serveConfigUpload answers an upload stream: the bridge admits it, then the
// reply head and the bridge's exchange follow on the stream.
func (s *serverSession) serveConfigUpload(lc *linkConn) {
	h := s.a.hooks.Load()
	if h == nil || h.ConfigUpload == nil {
		failLink(lc, true, wire.CodeUnsupported)
		return
	}
	serve := h.ConfigUpload(s)
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
	st, err := s.zc.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	lc := &linkConn{Stream: st, local: s.zc.LocalAddr(), remote: s.zc.RemoteAddr()}
	if err := st.SetPreamble(wire.AppendOpenInfo(nil, wire.OpConfigUpload)); err != nil {
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
