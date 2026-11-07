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

func handleUpdateRequest() {
	if RemoteUpdate == nil {
		log.Info("remote update requested but disabled")
		return
	}
	if !updating.CompareAndSwap(false, true) {
		log.Info("remote update requested while one is running")
		return
	}
	log.Info("remote update requested")
	go func() {
		defer updating.Store(false)
		if err := RemoteUpdate(); err != nil {
			log.Error("remote update failed", "err", err)
		}
	}()
}
