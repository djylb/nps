package common

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/djylb/nps/lib/logs"
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
	logs.Info("PProf debug listen on %s", ipPort)
}
