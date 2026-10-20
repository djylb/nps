package rate

import "github.com/djylb/byterate"

// The server-wide limit and traffic, charged by every connection that
// NewRateConn wraps, on top of the limit of the connection's client.
var (
	globalRate    byterate.Rate
	globalTraffic = byterate.NewSharedCounter()
	globalGroup   byterate.Group
)

// SetGlobalLimit sets the limit of all forwarded traffic together, in bytes
// per second; 0 or less means unlimited. It applies to open connections too.
func SetGlobalLimit(limitBps int64) {
	globalRate.Reset(max(limitBps, 0))
}

// GlobalLimit returns the limit of all forwarded traffic in bytes per
// second, or 0 if unlimited.
func GlobalLimit() int64 { return globalRate.Limit() }

// GlobalTraffic returns the bytes forwarded in total: in from visitors to
// clients, out from clients to visitors.
func GlobalTraffic() (in, out int64) {
	download, upload := globalTraffic.Total()
	return upload, download
}

// GlobalThroughput returns the forwarded bytes per second, in and out as
// GlobalTraffic counts them.
func GlobalThroughput() (inBps, outBps int64) {
	download, upload := globalTraffic.Throughput()
	return upload, download
}

// SetGlobalTraffic makes in and out the totals, such as persisted ones, which
// do not count as new traffic in GlobalThroughput.
func SetGlobalTraffic(in, out int64) {
	curIn, curOut := GlobalTraffic()
	globalTraffic.Restore(out-curOut, in-curIn)
}

// ResetGlobalTraffic starts the totals over from zero.
func ResetGlobalTraffic() { SetGlobalTraffic(0, 0) }

// RootGroup is the server level shared by all client and resource groups.
func RootGroup() *byterate.Group { return &globalGroup }

func init() {
	globalGroup.SetReadLimiter(&globalRate)
	globalGroup.SetWriteLimiter(&globalRate)
	globalGroup.SetTraffic(globalTraffic)
}
