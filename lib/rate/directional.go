package rate

import "github.com/djylb/byterate"

// Directional shares independent visitor-to-backend and backend-to-visitor
// buckets across all connections belonging to one tunnel or domain entry.
type Directional struct {
	upload   byterate.Rate
	download byterate.Rate
}

// SetLimits updates both byte-per-second limits without replacing the buckets.
func (d *Directional) SetLimits(upload, download int64) {
	if d.upload.Limit() != max(upload, 0) {
		d.upload.Reset(max(upload, 0))
	}
	if d.download.Limit() != max(download, 0) {
		d.download.Reset(max(download, 0))
	}
}

// Throughput returns current upload and download speeds in bytes per second.
func (d *Directional) Throughput() (int64, int64) {
	return d.upload.Throughput(), d.download.Throughput()
}
