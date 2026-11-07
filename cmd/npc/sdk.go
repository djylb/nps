//go:build sdk
// +build sdk

package main

import (
	"C"
	"context"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/obs"
	"github.com/djylb/nps/lib/version"
)

var cl *client.TRPClient

func init() {
	_ = logsetup.Apply(logsetup.Options{Output: "off", Level: "trace", Buffer: 64 << 10})
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
	_ = obs.Default.SetLevel(C.GoString(logsLevel))
}

//export Logs
func Logs() *C.char {
	return C.CString(obs.Default.TakeBuffered())
}

//export SetDnsServer
func SetDnsServer(dnsServer *C.char) {
	netaddr.SetCustomDNS(C.GoString(dnsServer))
}

func main() {
	// Need a main function to make CGO compile package as C shared library
}
