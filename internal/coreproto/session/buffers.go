package session

import "github.com/zmuxio/zmux-go"

// Bounds of a session's flow-control windows and send buffer, and with
// them the memory of its streams: one stream holds at most about
// streamWindow+sendBuffer on the sending side and streamWindow on the
// receiving side. 8 MiB keeps 320 Mbit/s at 200 ms RTT (window/RTT).
// zmux's automatic caps (64 MiB stream, 96 MiB session) let one bulk
// stream towards a slower receiver pile up some 100 MB on a relaying node.
const (
	streamWindow  = 8 << 20
	sessionWindow = 32 << 20
	sendBuffer    = 8 << 20
)

// boundBuffers applies the window and send-buffer bounds to c.
func boundBuffers(c *zmux.Config) *zmux.Config {
	c.MaxStreamReceiveWindow = streamWindow
	c.MaxConnectionReceiveWindow = sessionWindow
	c.MaxSendBuffer = sendBuffer
	return c
}
