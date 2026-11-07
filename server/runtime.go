package server

import (
	"math"
	stdnet "net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/netaddr"
	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/stats"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// dashboard caches the dashboard data and samples the network speed.
type dashboard struct {
	cacheMu         sync.RWMutex
	cache           map[string]interface{}
	lastRefresh     time.Time
	lastFullRefresh time.Time

	samplerOnce    sync.Once
	lastBytesSent  uint64
	lastBytesRecv  uint64
	lastSampleTime time.Time
	ioSendRate     atomic.Value // float64
	ioRecvRate     atomic.Value // float64
}

func (s *Server) startSpeedSampler() {
	d := &s.dash
	d.samplerOnce.Do(func() {
		if io1, _ := net.IOCounters(false); len(io1) > 0 {
			d.lastBytesSent = io1[0].BytesSent
			d.lastBytesRecv = io1[0].BytesRecv
		}
		d.lastSampleTime = time.Now()

		s.group.Go(func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				var now time.Time
				select {
				case now = <-ticker.C:
				case <-s.ctx.Done():
					return
				}
				if io2, _ := net.IOCounters(false); len(io2) > 0 {
					sent := io2[0].BytesSent
					recv := io2[0].BytesRecv
					elapsed := now.Sub(d.lastSampleTime).Seconds()

					// calculate bytes/sec
					rateSent := float64(stats.CounterDelta(sent, d.lastBytesSent)) / elapsed
					rateRecv := float64(stats.CounterDelta(recv, d.lastBytesRecv)) / elapsed

					d.ioSendRate.Store(rateSent)
					d.ioRecvRate.Store(rateRecv)

					d.lastBytesSent = sent
					d.lastBytesRecv = recv
					d.lastSampleTime = now
				}
			}
		})
	})
}

func (s *Server) initDashboardData() {
	s.startSpeedSampler()
	s.DashboardData(true)
}

// DashboardData returns the dashboard data, refreshed when force or when the
// cache is old.
func (s *Server) DashboardData(force bool) map[string]interface{} {
	cfg := servercfg.Current()
	d := &s.dash
	d.cacheMu.RLock()
	cached := d.cache
	lastR := d.lastRefresh
	lastFR := d.lastFullRefresh
	d.cacheMu.RUnlock()

	if cached != nil && !force && time.Since(lastFR) < 5*time.Second {
		if time.Since(lastR) < 1*time.Second {
			return cloneDashboardData(cached)
		}

		tcpCount := 0
		s.db.JsonDb.Clients.Range(func(key, value interface{}) bool {
			tcpCount += int(value.(*model.Client).CurrentConnections())
			return true
		})

		var cpuVal interface{}
		if cpuPercent, err := cpu.Percent(0, true); err == nil {
			var sum float64
			for _, v := range cpuPercent {
				sum += v
			}
			if n := len(cpuPercent); n > 0 {
				cpuVal = math.Round(sum / float64(n))
			}
		}

		var loadVal interface{}
		if loads, err := load.Avg(); err == nil {
			loadVal = loads.String()
		}

		var swapVal interface{}
		if swap, err := mem.SwapMemory(); err == nil {
			swapVal = math.Round(swap.UsedPercent)
		}

		var virtVal interface{}
		if vir, err := mem.VirtualMemory(); err == nil {
			virtVal = math.Round(vir.UsedPercent)
		}

		protoVals := map[string]int64{}
		if pcounters, err := net.ProtoCounters(nil); err == nil {
			for _, v := range pcounters {
				if val, ok := v.Stats["CurrEstab"]; ok {
					protoVals[v.Protocol] = val
				}
			}
		}
		if _, ok := protoVals["tcp"]; !ok {
			if conns, err := net.Connections("tcp"); err == nil {
				protoVals["tcp"] = int64(len(conns))
			}
		}
		if _, ok := protoVals["udp"]; !ok {
			if conns, err := net.Connections("udp"); err == nil {
				protoVals["udp"] = int64(len(conns))
			}
		}

		var ioSend, ioRecv interface{}
		if v, ok := d.ioSendRate.Load().(float64); ok {
			ioSend = v
		}
		if v, ok := d.ioRecvRate.Load().(float64); ok {
			ioRecv = v
		}

		upTime := runenv.GetRunTime()

		now := time.Now()

		d.cacheMu.Lock()
		dst := cloneDashboardData(d.cache)
		if dst == nil {
			dst = cloneDashboardData(cached)
		}
		dst["upTime"] = upTime
		dst["tcpCount"] = tcpCount
		if cpuVal != nil {
			dst["cpu"] = cpuVal
		}
		if loadVal != nil {
			dst["load"] = loadVal
		}
		if swapVal != nil {
			dst["swap_mem"] = swapVal
		}
		if virtVal != nil {
			dst["virtual_mem"] = virtVal
		}
		for k, v := range protoVals {
			dst[k] = v
		}
		if ioSend != nil {
			dst["io_send"] = ioSend
		}
		if ioRecv != nil {
			dst["io_recv"] = ioRecv
		}
		d.cache = dst
		d.lastRefresh = now
		d.cacheMu.Unlock()

		return cloneDashboardData(dst)
	}

	data := make(map[string]interface{})
	data["version"] = version.VERSION
	data["minVersion"] = GetMinVersion()
	data["hostCount"] = syncMapLen(&s.db.JsonDb.Hosts)
	data["clientCount"] = syncMapLen(&s.db.JsonDb.Clients)

	s.dealClientData()

	c := 0
	var in, out int64
	s.db.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*model.Client)
		if v.NoDisplay { // the public and visitor keys are not clients to show
			data["clientCount"] = data["clientCount"].(int) - 1
		}
		if v.IsConnect {
			c++
		}
		f := v.Flow.Values()
		in += f.InletFlow
		out += f.ExportFlow
		return true
	})
	data["clientOnlineCount"] = c
	data["inletFlowCount"] = int(in)
	data["exportFlowCount"] = int(out)

	var tcpN, udpN, secretN, socks5N, p2pN, httpN int
	s.db.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		t := value.(*model.Tunnel)
		switch t.Mode {
		case "tcp":
			tcpN++
		case "socks5":
			socks5N++
		case "httpProxy":
			httpN++
		case "mixProxy":
			if t.HttpProxy {
				httpN++
			}
			if t.Socks5Proxy {
				socks5N++
			}
		case "udp":
			udpN++
		case "p2p":
			p2pN++
		case "secret":
			secretN++
		}
		return true
	})
	data["tcpC"] = tcpN
	data["udpCount"] = udpN
	data["socks5Count"] = socks5N
	data["httpProxyCount"] = httpN
	data["secretCount"] = secretN
	data["p2pCount"] = p2pN

	data["bridgeType"] = cfg.Bridge.PrimaryType
	data["httpProxyPort"] = intStringOrEmpty(cfg.Network.HTTPProxyPort)
	data["httpsProxyPort"] = intStringOrEmpty(cfg.Network.HTTPSProxyPort)
	data["registeredIps"] = ipreg.Count()
	data["flowStoreInterval"] = intStringOrEmpty(cfg.Runtime.FlowStoreInterval)
	data["serverIp"] = netaddr.GetServerIp(connection.Network().P2PIP)
	data["serverIpv4"] = netaddr.GetOutboundIPv4().String()
	data["serverIpv6"] = netaddr.GetOutboundIPv6().String()
	data["p2pIp"] = connection.Network().P2PIP
	data["p2pPort"] = connection.Network().P2PPort
	data["p2pAddr"] = stdnet.JoinHostPort(netaddr.GetServerIp(connection.Network().P2PIP), strconv.Itoa(connection.Network().P2PPort))
	data["logLevel"] = cfg.Log.Level
	data["upTime"] = runenv.GetRunTime()
	data["upSecs"] = runenv.GetRunSecs()
	data["startTime"] = runenv.GetStartTime()

	tcpCount := 0
	s.db.JsonDb.Clients.Range(func(key, value interface{}) bool {
		tcpCount += int(value.(*model.Client).CurrentConnections())
		return true
	})
	data["tcpCount"] = tcpCount

	if cpuPercent, err := cpu.Percent(0, true); err == nil {
		var cpuAll float64
		for _, v := range cpuPercent {
			cpuAll += v
		}
		if n := len(cpuPercent); n > 0 {
			data["cpu"] = math.Round(cpuAll / float64(n))
		}
	}
	if loads, err := load.Avg(); err == nil {
		data["load"] = loads.String()
	}
	if swap, err := mem.SwapMemory(); err == nil {
		data["swap_mem"] = math.Round(swap.UsedPercent)
	}
	if vir, err := mem.VirtualMemory(); err == nil {
		data["virtual_mem"] = math.Round(vir.UsedPercent)
	}
	if pcounters, err := net.ProtoCounters(nil); err == nil {
		for _, v := range pcounters {
			if val, ok := v.Stats["CurrEstab"]; ok {
				data[v.Protocol] = val
			}
		}
	}
	if _, ok := data["tcp"]; !ok {
		if conns, err := net.Connections("tcp"); err == nil {
			data["tcp"] = int64(len(conns))
		}
	}
	if _, ok := data["udp"]; !ok {
		if conns, err := net.Connections("udp"); err == nil {
			data["udp"] = int64(len(conns))
		}
	}

	if v, ok := d.ioSendRate.Load().(float64); ok {
		data["io_send"] = v
	}
	if v, ok := d.ioRecvRate.Load().(float64); ok {
		data["io_recv"] = v
	}

	// chart
	deciles := stats.ChartDeciles()
	for i, v := range deciles {
		data["sys"+strconv.Itoa(i+1)] = v
	}

	now := time.Now()
	d.cacheMu.Lock()
	d.cache = data
	d.lastRefresh = now
	d.lastFullRefresh = now
	d.cacheMu.Unlock()

	return cloneDashboardData(data)
}

func GetVersion() string {
	return version.VERSION
}

func GetMinVersion() string {
	return bridge.MinClientVersion()
}

func GetCurrentYear() int {
	return time.Now().Year()
}

func intStringOrEmpty(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func cloneDashboardData(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneDashboardValue(value)
	}
	return dst
}

func cloneDashboardValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneDashboardData(v)
	case []interface{}:
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = cloneDashboardValue(item)
		}
		return items
	default:
		return value
	}
}

// syncMapLen counts the entries of m.
func syncMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}
