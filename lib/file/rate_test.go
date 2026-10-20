package file

import (
	"encoding/json"
	"testing"
)

func TestRateLimitsPersistenceAndHostSharing(t *testing.T) {
	h := &Host{Id: 1}
	h.SetRateLimits(12, 34)
	a, b := NewTunnelByHost(h, 80), NewTunnelByHost(h, 443)
	if a.RateLimiter() != h.RateLimiter() || b.RateLimiter() != h.RateLimiter() {
		t.Fatal("host TCP paths have separate buckets")
	}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Host
	if err = json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.UploadLimit != 12 || loaded.DownloadLimit != 34 {
		t.Fatalf("config lost: %s", data)
	}
	if loaded.runtimeRate != nil {
		t.Fatal("runtime persisted")
	}
	if loaded.RateLimiter() == h.RateLimiter() {
		t.Fatal("loaded runtime reused")
	}
	h.ClearUploadLimit()
	if h.UploadLimit != 0 || h.DownloadLimit != 34 {
		t.Fatal("clear changed opposite limit")
	}
}

func TestRateLimitsAcceptedCloneRetainsBuckets(t *testing.T) {
	db := newTestDb(t)
	original := &Tunnel{Id: 1, Mode: "tcp"}
	original.SetRateLimits(10, 20)
	if err := db.NewTask(original); err != nil {
		t.Fatal(err)
	}
	buckets := original.RateLimiter()
	clone := original.Clone()
	clone.SetRateLimits(30, 40)
	if original.UploadLimit != 10 {
		t.Fatal("editing clone modified original")
	}
	if err := db.UpdateTask(clone); err != nil {
		t.Fatal(err)
	}
	if clone.RateLimiter() != buckets {
		t.Fatal("accepted edit disconnected active rate buckets")
	}
	clone.ClearDownloadLimit()
	if clone.UploadLimit != 30 || clone.DownloadLimit != 0 {
		t.Fatal("clear changed opposite limit")
	}
}
