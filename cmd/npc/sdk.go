//go:build sdk
// +build sdk

package main

import (
	"C"
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/client/launch"
	"github.com/djylb/nps/internal/coreproto/connect"
	"github.com/djylb/nps/internal/logsetup"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/slogx"
)

var (
	stop    context.CancelFunc
	lastErr atomic.Pointer[string]
)

// setLastError records what LastError returns; nil clears it.
func setLastError(err error) {
	if err == nil {
		lastErr.Store(nil)
		return
	}
	msg := err.Error()
	lastErr.Store(&msg)
}

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
		setLastError(err)
		return 0
	}
	profileRun := func(ctx context.Context, p launch.Profile) error {
		_, err := p.Run(ctx, "", launch.Options{DisconnectTimeout: 30, P2PTimeout: 5})
		return err
	}
	if err := launch.Supervise(ctx, composer.Load, profileRun); err != nil && !errors.Is(err, context.Canceled) {
		setLastError(err)
	}
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
	setLastError(nil)
	locals, err := launch.ParseLocals(splitLines(C.GoString(localValues)))
	if err != nil {
		setLastError(err)
		return 0
	}
	return run(launch.Sources(splitLines(C.GoString(launchValues))), locals)
}

// StartClientByVerifyKey is the earlier form of StartClient, for one
// connection given by its parts.
//
//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	setLastError(nil)
	sources, err := launch.CommandLine{Server: C.GoString(serverAddr), VKey: C.GoString(verifyKey),
		Type: C.GoString(connType), Proxy: C.GoString(proxyUrl)}.Sources()
	if err == nil && len(sources) == 0 {
		err = errors.New("no server given")
	}
	if err != nil {
		setLastError(err)
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

// LastError returns why the last start failed or the client ended, or "".
//
//export LastError
func LastError() *C.char {
	if msg := lastErr.Load(); msg != nil {
		return C.CString(*msg)
	}
	return C.CString("")
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
