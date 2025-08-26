package tool

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/common"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

var (
	ports []int

	statusCap  = 1440
	ssMu       sync.RWMutex
	statBuf    = make([]map[string]interface{}, statusCap)
	statIdx    = 0
	statFilled = false

	startOnce sync.Once
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func StartSystemInfo() {
	if b, err := beego.AppConfig.Bool("system_info_display"); err == nil && b {
		startOnce.Do(func() {
			go getServerStatus()
		})
	}
}

func InitAllowPort() {
	p := beego.AppConfig.String("allow_ports")
	ports = common.GetPorts(p)
}

func TestServerPort(p int, m string) (b bool) {
	if m == "p2p" || m == "secret" {
		return true
	}
	if p > 65535 || p < 0 {
		return false
	}
	if len(ports) != 0 && !common.InIntArr(ports, p) {
		return false
	}
	if m == "udp" {
		b = common.TestUdpPort(p)
	} else {
		b = common.TestTcpPort(p)
	}
	return
}

func GenerateServerPort(m string) int {
	if len(ports) > 0 {
		for _, idx := range rand.Perm(len(ports)) {
			p := ports[idx]
			if p == 0 {
				continue
			}
			if TestServerPort(p, m) {
				return p
			}
		}
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := rand.Intn(65535-1024+1) + 1024 // [1024, 65535]
			if TestServerPort(serverPort, m) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			if TestServerPort(p, m) {
				return p
			}
		}
	}
	return 0
}

func statusCount() int {
	ssMu.RLock()
	defer ssMu.RUnlock()
	if statFilled {
		return statusCap
	}
	return statIdx
}

func StatusSnapshot() []map[string]interface{} {
	ssMu.RLock()
	defer ssMu.RUnlock()

	if !statFilled {
		out := make([]map[string]interface{}, statIdx)
		copy(out, statBuf[:statIdx])
		return out
	}
	out := make([]map[string]interface{}, statusCap)
	copy(out, statBuf[statIdx:])
	copy(out[statusCap-statIdx:], statBuf[:statIdx])
	return out
}

func ChartDeciles() []map[string]interface{} {
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
		out := make([]map[string]interface{}, n)
		for i := 0; i < n; i++ {
			out[i] = statBuf[(start+i)%statusCap]
		}
		return out
	}
	out := make([]map[string]interface{}, 10)
	for i := 0; i < 10; i++ {
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

		m := make(map[string]interface{}, 12)

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
				m["io_send"] = (io2[0].BytesSent - io1[0].BytesSent) * 2
				m["io_recv"] = (io2[0].BytesRecv - io1[0].BytesRecv) * 2
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
