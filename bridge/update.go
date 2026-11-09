package bridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/version"
)

// updateRequestTimeout bounds writing an update request to a client.
const updateRequestTimeout = 10 * time.Second

var (
	// ErrClientOffline is returned by RequestClientUpdate for a client that
	// has no connected npc.
	ErrClientOffline = errors.New("client is offline")
	// ErrUpdateUnsupported is returned by RequestClientUpdate when no
	// connected npc of the client acts on update requests.
	ErrUpdateUnsupported = fmt.Errorf("client version does not support remote update, npc %s or newer is required", RemoteUpdateMinVersion)
)

// RemoteUpdateMinVersion is the oldest npc release that acts on update
// requests.
const RemoteUpdateMinVersion = legacy.RemoteUpdateMinVersion

// updateRequest is the body of an update request; it carries no fields yet.
type updateRequest struct{}

// latestVersion is the version a node is compared with to tell whether it can
// be updated: the latest release as the server knows it, or while that is
// unknown the version of the server itself.
func (s *Bridge) latestVersion() string {
	if s.latest != nil {
		if v := s.latest(); v != "" {
			return v
		}
	}
	return version.VERSION
}

// canUpdate reports whether the node acts on an update request and runs a
// version older than latest. The npc of the old protocol shows what its
// version can do; whether it disabled remote update stays unknown.
func (n *Node) canUpdate(latest string) bool {
	if cs := n.coreSession(); cs != nil {
		if !cs.CanUpdate() {
			return false
		}
	} else if n.GetSignal() == nil || !n.Caps().RemoteUpdate {
		return false
	}
	_, v := n.versions()
	return version.Compare(v, latest) < 0
}

// ClientCanUpdate reports whether at least one connected npc of client id can
// be updated; it reads what the bridge holds and queries nothing.
func (s *Bridge) ClientCanUpdate(id int) bool {
	v, ok := s.Client.Load(id)
	if !ok {
		return false
	}
	latest := s.latestVersion()
	for _, node := range v.(*Client).nodeList() {
		if node.canUpdate(latest) {
			return true
		}
	}
	return false
}

// RequestClientUpdate asks every connected npc of client id that can be
// updated (see ClientCanUpdate) to update itself to the latest release. It
// returns how many were asked.
func (s *Bridge) RequestClientUpdate(id int) (int, error) {
	v, ok := s.Client.Load(id)
	if !ok {
		return 0, ErrClientOffline
	}
	c := v.(*Client)
	var sent, unsupported int
	var lastErr error
	latest := s.latestVersion()
	for _, node := range c.nodeList() {
		if !node.canUpdate(latest) {
			unsupported++
			continue
		}
		if cs := node.coreSession(); cs != nil {
			ctx, cancel := context.WithTimeout(s.scope.Context(), updateRequestTimeout)
			err := cs.RequestUpdate(ctx)
			cancel()
			switch {
			case err == nil:
				sent++
			case coreapi.IsNotAllowed(err):
				unsupported++
			default:
				lastErr = err
			}
			continue
		}
		if err := node.sendSignal(legacy.UpdateRequest, updateRequest{}, updateRequestTimeout); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	switch {
	case sent > 0:
		log.Info("update requested", "client", id, "count", sent)
		return sent, nil
	case lastErr != nil:
		return 0, fmt.Errorf("send update request: %w", lastErr)
	case unsupported > 0:
		return 0, ErrUpdateUnsupported
	}
	return 0, ErrClientOffline
}
