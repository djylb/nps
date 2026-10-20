package rate

import (
	"sync"

	"github.com/djylb/byterate"
)

// Directional shares independent visitor-to-backend and backend-to-visitor
// buckets across all connections belonging to one tunnel or domain entry.
type Directional struct {
	upload    byterate.Rate
	download  byterate.Rate
	group     byterate.Group
	groupOnce sync.Once
	parentMu  sync.Mutex
	pinned    bool
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
	download, upload := d.Group(nil).Traffic().Throughput()
	return upload, download
}

// Group is the tunnel/domain level. A nil parent keeps its current ancestry.
func (d *Directional) Group(parent *byterate.Group) *byterate.Group {
	d.groupOnce.Do(func() { d.group.SetReadLimiter(&d.download); d.group.SetWriteLimiter(&d.upload) })
	if parent != nil && parent != &d.group {
		d.parentMu.Lock()
		d.pinned = true
		if d.group.Parent() != parent {
			_ = d.group.SetParent(parent)
		}
		d.parentMu.Unlock()
	}
	return &d.group
}

// connectionGroup fills in ancestry for standalone wrappers. A model's explicit
// Group binding wins, so a cached dial with an old client cannot undo an edit.
func (d *Directional) connectionGroup(parent *byterate.Group) *byterate.Group {
	g := d.Group(nil)
	d.parentMu.Lock()
	defer d.parentMu.Unlock()
	if !d.pinned && parent != g && g.Parent() != parent {
		_ = g.SetParent(parent)
	}
	return g
}
