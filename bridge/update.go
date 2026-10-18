package bridge

import (
	"errors"
	"fmt"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
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
	ErrUpdateUnsupported = fmt.Errorf("client version does not support remote update, npc %s or newer is required", version.RemoteUpdateMinVersion)
)

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
	c.nodes.Range(func(_, value any) bool {
		node, ok := value.(*Node)
		if !ok || node.GetSignal() == nil {
			return true
		}
		if _, vs := node.versions(); !version.SupportsRemoteUpdate(vs) {
			unsupported++
			return true
		}
		if err := node.sendSignal(common.UpdateRequest, updateRequest{}, updateRequestTimeout); err != nil {
			lastErr = err
			return true
		}
		sent++
		return true
	})
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
