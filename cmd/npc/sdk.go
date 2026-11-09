//go:build sdk
// +build sdk

package main

import (
	"C"
	"context"
	"strings"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
)

var stop context.CancelFunc

func init() {
	_ = logsetup.Apply(logsetup.Options{Output: "off", Level: "trace", Memory: 1000})
}

// run blocks on sources and locals, the path of -launch and -L, until the
// first connection ends: the caller decides about reconnecting. It returns
// 0 when the input is invalid, 1 when the client ran and ended.
func run(sources []launch.Source, locals []launch.Local) int {
	if stop != nil {
		stop()
	}
	var ctx context.Context
	ctx, stop = context.WithCancel(context.Background())
	connect.Install()
	client.AutoReconnect = false
	composer := launch.NewComposer(sources, locals)
	if _, err := composer.Load(ctx); err != nil {
		return 0
	}
	profileRun := func(ctx context.Context, p launch.Profile) error {
		_, err := p.Run(ctx, "", launch.Options{DisconnectTimeout: 30, P2PTimeout: 5})
		return err
	}
	_ = launch.Supervise(ctx, composer.Load, profileRun)
	return 1
}

// splitLines is the values of a newline-separated argument.
func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// StartClient runs -launch values and -L values, one per line. It blocks
// until the client ends.
//
//export StartClient
func StartClient(launchValues, localValues *C.char) int {
	locals, err := launch.ParseLocals(splitLines(C.GoString(localValues)))
	if err != nil {
		return 0
	}
	return run(launch.LaunchSources(splitLines(C.GoString(launchValues))), locals)
}

// StartClientByVerifyKey is the earlier form of StartClient, for one
// connection given by its parts.
//
//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	sources, err := launch.CommandLine{Server: C.GoString(serverAddr), VKey: C.GoString(verifyKey),
		Type: C.GoString(connType), Proxy: C.GoString(proxyUrl)}.Sources()
	if err != nil || len(sources) == 0 {
		return 0
	}
	return run(sources, nil)
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
