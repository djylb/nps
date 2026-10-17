// Package rate limits the bytes a client's connections transfer, on top of
// github.com/djylb/byterate.
package rate

import (
	"encoding/json"

	"github.com/djylb/byterate"
)

// Rate is a byte rate limiter shared by all connections of a client: their
// reads and writes are charged to the same bucket. A limit <= 0 means
// unlimited. The zero value is an unlimited, running Rate.
//
// Its JSON form is {"NowRate":...,"Limit":...}, the measured throughput and
// the limit in bytes per second. Decoding ignores both: they are runtime
// state, and the limit comes from the client's configuration.
type Rate struct {
	l byterate.Rate
}

type rateJSON struct {
	NowRate int64 `json:"NowRate"` // bytes/s
	Limit   int64 `json:"Limit"`   // bytes/s, 0 => unlimited
}

// NewRate returns a running Rate limited to limitBps bytes per second.
func NewRate(limitBps int64) *Rate {
	r := new(Rate)
	r.l.SetLimit(limitBps)
	return r
}

// Start undoes Stop, starting over with a full burst.
func (r *Rate) Start() {
	if r != nil {
		r.l.Enable()
	}
}

// Stop lets all traffic pass unlimited and unmetered until Start, and wakes
// waiting connections.
func (r *Rate) Stop() {
	if r != nil {
		r.l.Disable()
	}
}

// ResetLimit sets the limit, wakes waiting connections and starts over with a
// full burst.
func (r *Rate) ResetLimit(limitBps int64) {
	if r != nil {
		r.l.Reset(limitBps)
	}
}

// Limit returns the limit in bytes per second, or 0 if unlimited.
func (r *Rate) Limit() int64 {
	if r == nil {
		return 0
	}
	return r.l.Limit()
}

// Now returns the measured throughput in bytes per second.
func (r *Rate) Now() int64 {
	if r == nil {
		return 0
	}
	return r.l.Throughput()
}

func (r *Rate) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return json.Marshal(rateJSON{NowRate: r.Now(), Limit: r.Limit()})
}
