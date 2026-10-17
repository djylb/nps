package common

import (
	"encoding/binary"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/beevik/ntp"
	"github.com/djylb/nps/lib/logs"
)

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

func CalibrateTimeOffset(server string) (time.Duration, error) {
	if server == "" {
		return 0, nil
	}
	ntpTime, err := ntp.Time(server)
	if err != nil {
		return 0, err
	}
	return time.Until(ntpTime), nil
}

func TimeNow() time.Time {
	SyncTime()
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return time.Now().Add(timeOffset)
}

func SyncTime() {
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
	offset, err := CalibrateTimeOffset(srv)
	if err != nil {
		logs.Error("ntp[%s] sync failed: %v", srv, err)
	}
	timeMutex.Lock()
	timeOffset = offset
	timeMutex.Unlock()
	if offset != 0 {
		logs.Info("ntp[%s] offset=%v", srv, offset)
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

// TimestampToBytes 8bit
func TimestampToBytes(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}

// BytesToTimestamp 8bit
func BytesToTimestamp(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}
