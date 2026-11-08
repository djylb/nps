//go:build sdk
// +build sdk

package main

import (
	"C"
	"context"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
)

var stop context.CancelFunc

func init() {
	_ = logsetup.Apply(logsetup.Options{Output: "off", Level: "trace", Memory: 1000})
}

//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	if stop != nil {
		stop()
	}
	var ctx context.Context
	ctx, stop = context.WithCancel(context.Background())
	connect.Install()
	cfg := config.NewCommonConfig()
	cfg.Server, cfg.VKey, cfg.Tp, cfg.ProxyUrl = C.GoString(serverAddr), C.GoString(verifyKey), C.GoString(connType), C.GoString(proxyUrl)
	if err := client.RunCoreProfile(ctx, cfg, false); err != nil {
		return 0
	}
	return 1
}

//export GetClientStatus
func GetClientStatus() int {
	return int(client.NowStatus.Load())
}

//export CloseClient
func CloseClient() {
	if stop != nil {
		stop()
	}
}

//export Version
func Version() *C.char {
	return C.CString(version.VERSION)
}

//export SetLogsLevel
func SetLogsLevel(logsLevel *C.char) {
	_ = slogx.Default.Levels().Set(C.GoString(logsLevel))
}

//export Logs
func Logs() *C.char {
	return C.CString(takeLogs())
}

//export SetDnsServer
func SetDnsServer(dnsServer *C.char) {
	netaddr.SetCustomDNS(C.GoString(dnsServer))
}

func main() {
	// Need a main function to make CGO compile package as C shared library
}

// takeLogs returns the log lines not returned before, as logfmt.
func takeLogs() string {
	m := slogx.Default.Memory()
	if m == nil {
		return ""
	}
	var b []byte
	for _, e := range m.Take() {
		r := e.Record()
		b = slogx.TextEncoder{}.Encode(b, &r)
	}
	return string(b)
}
