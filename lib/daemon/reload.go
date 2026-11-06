//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/djylb/nps/lib/logs"
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
			old := servercfg.Current()
			if err := servercfg.Reload(); err != nil {
				logs.Warn("Config reload failed: %v", err)
				continue
			}
			if names := servercfg.RestartRequired(old, servercfg.Current()); len(names) > 0 {
				logs.Warn("Config reloaded, but these changes need a restart: %s", strings.Join(names, ", "))
			}
			server.SetWebHandler(routers.Init())
		}
	}()
}
