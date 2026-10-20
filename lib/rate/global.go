package rate

import "github.com/djylb/byterate"

// The server-wide limit and traffic, charged by every connection that
// NewRateConn wraps, on top of the limit of the connection's client.
var (
	globalRate    byterate.Rate
	globalTraffic = byterate.NewSharedCounter()
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
func GlobalTraffic() (in, out int64) { return globalTraffic.Total() }

// GlobalThroughput returns the forwarded bytes per second, in and out as
// GlobalTraffic counts them.
func GlobalThroughput() (inBps, outBps int64) { return globalTraffic.Throughput() }

// SetGlobalTraffic makes in and out the totals, such as persisted ones, which
// do not count as new traffic in GlobalThroughput.
func SetGlobalTraffic(in, out int64) {
	curIn, curOut := globalTraffic.Total()
	globalTraffic.Restore(in-curIn, out-curOut)
}

// ResetGlobalTraffic starts the totals over from zero.
func ResetGlobalTraffic() { SetGlobalTraffic(0, 0) }

// limiters returns the limiters of the reads and the writes of a client's
// connection to its npc: the client's Rate and the global Rate, which both
// directions share, and the global traffic, where what the server reads
// from the npc goes out to visitors and what it writes comes in from them.
func limiters(r *Rate) (read, write byterate.Limiter) {
	var client, in, out byterate.Limiter
	if r != nil {
		client = &r.l
		in, out = r.traffic.Inbound(), r.traffic.Outbound()
	}
	return byterate.NewHierarchy(client, out, &globalRate, globalTraffic.Outbound()),
		byterate.NewHierarchy(client, in, &globalRate, globalTraffic.Inbound())
}
