package main

import (
	"C"

	"github.com/beego/beego/logs"
	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/version"
)

var cl *client.TRPClient

//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	_ = logs.SetLogger("store")
	if cl != nil {
		cl.Close()
	}
	cl = client.NewRPClient(C.GoString(serverAddr), C.GoString(verifyKey), C.GoString(connType), C.GoString(proxyUrl), nil, 60)
	cl.Start()
	return 1
}

//export GetClientStatus
func GetClientStatus() int {
	return client.NowStatus
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

//export Logs
func Logs() *C.char {
	return C.CString(common.GetLogMsg())
}

//export SetTlsEnable
func SetTlsEnable(enable bool) {
	client.SetTlsEnable(enable)
}

//export SetDnsServer
func SetDnsServer(dnsServer *C.char) {
	common.SetCustomDNS(C.GoString(dnsServer))
}

func main() {
	// Need a main function to make CGO compile package as C shared library
}
