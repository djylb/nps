package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/beevik/ntp"
	"github.com/djylb/nps/lib/logs"
)

func GetSyncMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}

func RandomBytes(maxLen int) ([]byte, error) {
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(maxLen+1)))
	if err != nil {
		return nil, err
	}
	n := int(nBig.Int64())
	buf := make([]byte, n)
	if _, err = rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
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

var (
	timeOffset   time.Duration
	ntpServer    string
	syncInterval = 5 * time.Minute
	lastSyncMono time.Time
	timeMutex    sync.RWMutex
	syncCh       = make(chan struct{}, 1)
)

func SetNtpServer(server string)     { timeMutex.Lock(); ntpServer = server; timeMutex.Unlock() }
func SetNtpInterval(d time.Duration) { timeMutex.Lock(); syncInterval = d; timeMutex.Unlock() }

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

func TimeOffset() time.Duration { timeMutex.RLock(); defer timeMutex.RUnlock(); return timeOffset }

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

func TimestampToBytes(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}

func BytesToTimestamp(b []byte) int64 { return int64(binary.BigEndian.Uint64(b)) }

func ValidatePoW(bits int, parts ...string) bool {
	if bits < 1 || bits > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "")))
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if sum[i] != 0 {
			return false
		}
	}
	remBits := bits % 8
	if remBits > 0 {
		mask := byte(0xFF << (8 - remBits))
		if (sum[fullBytes] & mask) != 0 {
			return false
		}
	}
	return true
}

func IsTrustedProxy(list, ipStr string) bool {
	if list == "" || ipStr == "" {
		return false
	}
	ipStr = strings.TrimSpace(ipStr)
	if h, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = h
	}
	if strings.HasPrefix(ipStr, "[") && strings.HasSuffix(ipStr, "]") {
		ipStr = ipStr[1 : len(ipStr)-1]
	}
	if i := strings.LastIndex(ipStr, "%"); i != -1 {
		ipStr = ipStr[:i]
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	for _, raw := range strings.Split(list, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") {
			if _, cidr, err := net.ParseCIDR(entry); err == nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		if strings.Contains(entry, "*") {
			if ip4 == nil {
				continue
			}
			pSegs := strings.Split(entry, ".")
			if len(pSegs) != 4 {
				continue
			}
			matched := true
			for i := 0; i < 4; i++ {
				if pSegs[i] == "*" {
					continue
				}
				n, err := strconv.Atoi(pSegs[i])
				if err != nil || n < 0 || n > 255 || int(ip4[i]) != n {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
			continue
		}
		if e := net.ParseIP(entry); e != nil && e.Equal(ip) {
			return true
		}
	}
	return false
}
