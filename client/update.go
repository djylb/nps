package client

import (
	"sync/atomic"
)

// RemoteUpdate updates npc to the latest release and restarts it when the
// server requests it. Nil, the default, makes npc ignore such requests.
var RemoteUpdate func() error

// updating keeps a single update running, also across the connections to
// several servers.
var updating atomic.Bool

// AcceptUpdate starts the update the server asked for, and reports whether it
// did: refused by default, and while one is running.
func AcceptUpdate() bool {
	if RemoteUpdate == nil {
		log.Info("remote update refused", "reason", "disabled")
		return false
	}
	if !updating.CompareAndSwap(false, true) {
		log.Info("remote update refused", "reason", "running")
		return false
	}
	log.Info("remote update requested")
	go func() {
		defer updating.Store(false)
		if err := RemoteUpdate(); err != nil {
			log.Error("remote update failed", "err", err)
		}
	}()
	return true
}

func handleUpdateRequest() { _ = AcceptUpdate() }
