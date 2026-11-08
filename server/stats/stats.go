// Package stats samples the load, memory and traffic of the server for the
// dashboard.
package stats

import (
	"math"
	"sync"
	"time"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

var (
	statusCap  = 1440
	ssMu       sync.RWMutex
	statBuf    = make([]map[string]any, statusCap)
	statIdx    = 0
	statFilled = false

	startOnce sync.Once
)

func StartSystemInfo() {
	if servercfg.Current().Feature.SystemInfoDisplay {
		startOnce.Do(func() {
			go getServerStatus()
		})
	}
}

func statusCount() int {
	ssMu.RLock()
	defer ssMu.RUnlock()
	if statFilled {
		return statusCap
	}
	return statIdx
}

func StatusSnapshot() []map[string]any {
	ssMu.RLock()
	defer ssMu.RUnlock()

	if !statFilled {
		out := make([]map[string]any, statIdx)
		copy(out, statBuf[:statIdx])
		return out
	}
	out := make([]map[string]any, statusCap)
	copy(out, statBuf[statIdx:])
	copy(out[statusCap-statIdx:], statBuf[:statIdx])
	return out
}

func ChartDeciles() []map[string]any {
	ssMu.RLock()
	defer ssMu.RUnlock()

	var n, start int
	if statFilled {
		n, start = statusCap, statIdx
	} else {
		n, start = statIdx, 0
	}
	if n == 0 {
		return nil
	}
	if n <= 10 {
		out := make([]map[string]any, n)
		for i := 0; i < n; i++ {
			out[i] = statBuf[(start+i)%statusCap]
		}
		return out
	}
	out := make([]map[string]any, 10)
	for i := range 10 {
		pos := (i * (n - 1)) / 9
		idx := (start + pos) % statusCap
		out[i] = statBuf[idx]
	}
	return out
}

func getServerStatus() {
	for {
		if statusCount() < 10 {
			time.Sleep(1 * time.Second)
		} else {
			time.Sleep(1 * time.Minute)
		}

		m := make(map[string]any, 12)

		// CPU
		if cpuPercent, err := cpu.Percent(0, true); err == nil && len(cpuPercent) > 0 {
			var sum float64
			for _, v := range cpuPercent {
				sum += v
			}
			m["cpu"] = math.Round(sum / float64(len(cpuPercent)))
		}

		// Load
		if loads, err := load.Avg(); err == nil {
			m["load1"] = loads.Load1
			m["load5"] = loads.Load5
			m["load15"] = loads.Load15
		}

		// Mem
		if swap, err := mem.SwapMemory(); err == nil {
			m["swap_mem"] = math.Round(swap.UsedPercent)
		}
		if vir, err := mem.VirtualMemory(); err == nil {
			m["virtual_mem"] = math.Round(vir.UsedPercent)
		}

		// Conn
		if pcounters, err := net.ProtoCounters(nil); err == nil {
			for _, v := range pcounters {
				if val, ok := v.Stats["CurrEstab"]; ok {
					m[v.Protocol] = val // int64
				}
			}
		}

		// IO
		if io1, err := net.IOCounters(false); err == nil {
			time.Sleep(500 * time.Millisecond)
			if io2, err2 := net.IOCounters(false); err2 == nil && len(io1) > 0 && len(io2) > 0 {
				m["io_send"] = CounterDelta(io2[0].BytesSent, io1[0].BytesSent) * 2
				m["io_recv"] = CounterDelta(io2[0].BytesRecv, io1[0].BytesRecv) * 2
			}
		}

		// Time
		t := time.Now()
		m["time"] = t.Format("15:04:05")

		ssMu.Lock()
		statBuf[statIdx] = m
		statIdx = (statIdx + 1) % statusCap
		if statIdx == 0 {
			statFilled = true
		}
		ssMu.Unlock()
	}
}

// CounterDelta returns how far a monotonically increasing counter advanced
// from prev to cur. A counter that went backwards was reset (for example the
// interface was recreated), and the interval counts as zero.
func CounterDelta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}
