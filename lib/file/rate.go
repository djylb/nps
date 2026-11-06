package file

import (
	"math"
	"sync"

	"github.com/djylb/nps/lib/rate"
)

// RateLimits stores per-entry limits in KiB/s. Runtime buckets are never persisted.
// Upload runs from visitors to the backend; download runs in the other direction.
type RateLimits struct {
	UploadLimit   int
	DownloadLimit int
	rateMu        sync.Mutex
	runtimeRate   *rate.Directional
}

func limitBytes(k int) int64 {
	if k <= 0 {
		return 0
	}
	if uint64(k) > uint64(math.MaxInt64/1024) {
		return math.MaxInt64
	}
	return int64(k) * 1024
}

// RateLimiter returns the shared buckets, initializing loaded configuration lazily.
func (r *RateLimits) RateLimiter() *rate.Directional {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if r.runtimeRate == nil {
		r.runtimeRate = new(rate.Directional)
		r.runtimeRate.SetLimits(limitBytes(r.UploadLimit), limitBytes(r.DownloadLimit))
	}
	return r.runtimeRate
}

// SetRateLimits applies configuration to existing connections as well as new ones.
func (r *RateLimits) SetRateLimits(upload, download int) {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	r.UploadLimit, r.DownloadLimit = max(upload, 0), max(download, 0)
	if r.runtimeRate != nil {
		r.runtimeRate.SetLimits(limitBytes(r.UploadLimit), limitBytes(r.DownloadLimit))
	}
}

// ClearUploadLimit removes only the upload limit.
func (r *RateLimits) ClearUploadLimit() {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	r.UploadLimit = 0
	if r.runtimeRate != nil {
		r.runtimeRate.SetLimits(0, limitBytes(r.DownloadLimit))
	}
}

// ClearDownloadLimit removes only the download limit.
func (r *RateLimits) ClearDownloadLimit() {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	r.DownloadLimit = 0
	if r.runtimeRate != nil {
		r.runtimeRate.SetLimits(limitBytes(r.UploadLimit), 0)
	}
}

// CurrentRates returns measured upload and download speeds in bytes per second.
func (r *RateLimits) CurrentRates() (int64, int64) {
	r.rateMu.Lock()
	d := r.runtimeRate
	r.rateMu.Unlock()
	if d == nil {
		return 0, 0
	}
	return d.Throughput()
}

// copyConfig makes an independent editable configuration, without copying locks.
func (r *RateLimits) copyConfig() RateLimits {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	return RateLimits{UploadLimit: r.UploadLimit, DownloadLimit: r.DownloadLimit}
}

// shared lets a host's derived TCP tasks use the same buckets and speed meter.
func (r *RateLimits) shared() RateLimits {
	d := r.RateLimiter()
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	return RateLimits{UploadLimit: r.UploadLimit, DownloadLimit: r.DownloadLimit, runtimeRate: d}
}

// adopt keeps active connections on the same buckets when an edited task replaces
// the stored object. Call only after validation has accepted the edit.
func (r *RateLimits) adopt(old *RateLimits) {
	d := old.RateLimiter()
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	r.runtimeRate = d
	d.SetLimits(limitBytes(r.UploadLimit), limitBytes(r.DownloadLimit))
}

// connectionCount reads the library group without allocating state for idle rows.
func (r *RateLimits) connectionCount() int32 {
	r.rateMu.Lock()
	d := r.runtimeRate
	r.rateMu.Unlock()
	if d == nil {
		return 0
	}
	return int32(d.Group(nil).Conns())
}
