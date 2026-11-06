//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/web/routers"
)

func init() {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR1)
	go func() {
		for {
			<-s
			if err := servercfg.Reload(); err != nil {
				continue
			}
			server.SetWebHandler(routers.Init())
		}
	}()
}
