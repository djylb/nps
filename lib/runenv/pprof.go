package runenv

import (
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/netip"

	"github.com/djylb/slogx"
)

// InitPProfByAddr serves pprof on addr, host:port; an empty host is the
// loopback address, as the profiles show the process's memory.
func InitPProfByAddr(addr string) {
	if len(addr) == 0 {
		return
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil && host == "" {
		addr = net.JoinHostPort("127.0.0.1", port)
	} else if ip, perr := netip.ParseAddr(host); err == nil && host != "localhost" && (perr != nil || !ip.IsLoopback()) {
		slogx.Module("runenv").Warn("pprof listens beyond this host: anyone who reaches it can read the process memory", "addr", addr)
	}
	runPProf(addr)
}

func runPProf(ipPort string) {
	go func() {
		_ = http.ListenAndServe(ipPort, nil)
	}()
	slogx.Module("runenv").Info("pprof listening", "addr", ipPort)
}
