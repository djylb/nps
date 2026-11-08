// Package clock corrects the local clock by an NTP offset and sets the
// process time zone.
package clock

import (
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/beevik/ntp"
	"github.com/djylb/slogx"
)

var log = slogx.Module("clock")

var (
	timeOffset   time.Duration
	ntpServer    string
	syncInterval = 5 * time.Minute
	lastSyncMono time.Time
	timeMutex    sync.RWMutex
	syncCh       = make(chan struct{}, 1)
)

func SetNtpServer(server string) {
	timeMutex.Lock()
	defer timeMutex.Unlock()
	ntpServer = server
}

func SetNtpInterval(d time.Duration) {
	timeMutex.Lock()
	defer timeMutex.Unlock()
	syncInterval = d
}

func calibrateOffset(server string) (time.Duration, error) {
	if server == "" {
		return 0, nil
	}
	ntpTime, err := ntp.Time(server)
	if err != nil {
		return 0, err
	}
	return time.Until(ntpTime), nil
}

func Now() time.Time {
	Sync()
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return time.Now().Add(timeOffset)
}

func Sync() {
	timeMutex.RLock()
	srv, last, interval := ntpServer, lastSyncMono, syncInterval
	timeMutex.RUnlock()
	if srv == "" || (!last.IsZero() && time.Since(last) < interval) {
		return
	}
	select {
	case syncCh <- struct{}{}:
		defer func() { <-syncCh }()
	default:
		return
	}
	now := time.Now()
	timeMutex.Lock()
	lastSyncMono = now
	timeMutex.Unlock()
	offset, err := calibrateOffset(srv)
	if err != nil {
		log.Warn("ntp sync failed", "addr", srv, "err", err)
		// Keep the last successful correction when the NTP server is unavailable.
		return
	}
	timeMutex.Lock()
	timeOffset = offset
	timeMutex.Unlock()
	if offset != 0 {
		log.Info("ntp synced", "addr", srv, "offset_ms", offset.Milliseconds())
	}
}

func SetTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	time.Local = loc
	return nil
}
