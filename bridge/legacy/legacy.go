// Package legacy is nps's one door to the wire protocol that released npc
// speak: it alone turns protocol messages into nps's own types. The rest of
// nps knows the protocol only through what this package exports, so a future
// protocol is a second package beside it. It goes when the last client that
// speaks this protocol does.
package legacy

import (
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/tunnelwire/mux"
)

type muxLogger struct{}

func (muxLogger) Println(v ...any)               { logs.Println(v...) }
func (muxLogger) Printf(format string, v ...any) { logs.Printf(format, v...) }
func (muxLogger) Warn(format string, v ...any)   { logs.Warn(format, v...) }
func (muxLogger) Error(format string, v ...any)  { logs.Error(format, v...) }

func init() { mux.SetLogger(muxLogger{}) }
