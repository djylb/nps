package runenv

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/djylb/nps/lib/obs"
)

func InitPProfByAddr(addr string) {
	if len(addr) > 0 {
		runPProf(addr)
	}
}

func runPProf(ipPort string) {
	go func() {
		_ = http.ListenAndServe(ipPort, nil)
	}()
	obs.Module("runenv").Info("pprof listening", "addr", ipPort)
}
