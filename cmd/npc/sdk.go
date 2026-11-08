//go:build sdk
// +build sdk

package main

import (
	"C"
	"context"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
)

var cl *client.TRPClient

func init() {
	_ = logsetup.Apply(logsetup.Options{Output: "off", Level: "trace", Memory: 1000})
}

//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	if cl != nil {
		cl.Close()
	}
	cl = client.NewRPClient(C.GoString(serverAddr), C.GoString(verifyKey), C.GoString(connType), C.GoString(proxyUrl), "", "", nil, 60, nil)
	cl.Start(context.Background())
	return 1
}

//export GetClientStatus
func GetClientStatus() int {
	return int(client.NowStatus.Load())
}

//export CloseClient
func CloseClient() {
	if cl != nil {
		cl.Close()
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
