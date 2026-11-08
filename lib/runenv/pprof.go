package runenv

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/djylb/slogx"
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
	slogx.Module("runenv").Info("pprof listening", "addr", ipPort)
}
