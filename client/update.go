package client

import (
	"sync/atomic"

	"github.com/djylb/nps/lib/logs"
)

// RemoteUpdate updates npc to the latest release and restarts it when the
// server requests it. Nil, the default, makes npc ignore such requests.
var RemoteUpdate func() error

// updating keeps a single update running, also across the connections to
// several servers.
var updating atomic.Bool

func handleUpdateRequest() {
	if RemoteUpdate == nil {
		logs.Info("The server requested an update, but remote update is disabled")
		return
	}
	if !updating.CompareAndSwap(false, true) {
		logs.Info("The server requested an update while one is running")
		return
	}
	logs.Info("The server requested an update")
	go func() {
		defer updating.Store(false)
		if err := RemoteUpdate(); err != nil {
			logs.Error("Remote update failed: %v", err)
		}
	}()
}
