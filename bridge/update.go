package bridge

import (
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/logs"
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

// RequestClientUpdate asks every connected npc of client id that supports it
// to update itself to the latest release. It returns how many were asked.
func (s *Bridge) RequestClientUpdate(id int) (int, error) {
	v, ok := s.Client.Load(id)
	if !ok {
		return 0, ErrClientOffline
	}
	c := v.(*Client)
	var sent, unsupported int
	var lastErr error
	for _, node := range c.nodeList() {
		if node.GetSignal() == nil {
			continue
		}
		if !node.Caps().RemoteUpdate {
			unsupported++
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
		logs.Info("Update requested from %d npc of client %d", sent, id)
		return sent, nil
	case lastErr != nil:
		return 0, fmt.Errorf("send update request: %w", lastErr)
	case unsupported > 0:
		return 0, ErrUpdateUnsupported
	}
	return 0, ErrClientOffline
}
