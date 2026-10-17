package server

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/tool"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

var (
	// Cache
	cacheMu         sync.RWMutex
	dashboardCache  map[string]interface{}
	lastRefresh     time.Time
	lastFullRefresh time.Time

	// Net IO
	samplerOnce    sync.Once
	lastBytesSent  uint64
	lastBytesRecv  uint64
	lastSampleTime time.Time
	ioSendRate     atomic.Value // float64
	ioRecvRate     atomic.Value // float64
)

func startSpeedSampler() {
	samplerOnce.Do(func() {
		if io1, _ := net.IOCounters(false); len(io1) > 0 {
			lastBytesSent = io1[0].BytesSent
			lastBytesRecv = io1[0].BytesRecv
		}
		lastSampleTime = time.Now()

		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for now := range ticker.C {
				if io2, _ := net.IOCounters(false); len(io2) > 0 {
					sent := io2[0].BytesSent
					recv := io2[0].BytesRecv
					elapsed := now.Sub(lastSampleTime).Seconds()

					// calculate bytes/sec
					rateSent := float64(tool.CounterDelta(sent, lastBytesSent)) / elapsed
					rateRecv := float64(tool.CounterDelta(recv, lastBytesRecv)) / elapsed

					ioSendRate.Store(rateSent)
					ioRecvRate.Store(rateRecv)

					lastBytesSent = sent
					lastBytesRecv = recv
					lastSampleTime = now
				}
			}
		}()
	})
}

func InitDashboardData() {
	startSpeedSampler()
	GetDashboardData(true)
	//return
}

func GetDashboardData(force bool) map[string]interface{} {
	cfg := servercfg.Current()
	cacheMu.RLock()
	cached := dashboardCache
	lastR := lastRefresh
	lastFR := lastFullRefresh
	cacheMu.RUnlock()

	if cached != nil && !force && time.Since(lastFR) < 5*time.Second {
		if time.Since(lastR) < 1*time.Second {
			return cloneDashboardData(cached)
		}

		tcpCount := 0
		file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
			tcpCount += int(value.(*file.Client).NowConn)
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
		if v, ok := ioSendRate.Load().(float64); ok {
			ioSend = v
		}
		if v, ok := ioRecvRate.Load().(float64); ok {
			ioRecv = v
		}

		upTime := common.GetRunTime()

		now := time.Now()

		cacheMu.Lock()
		dst := cloneDashboardData(dashboardCache)
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
		dashboardCache = dst
		lastRefresh = now
		cacheMu.Unlock()

		return cloneDashboardData(dst)
	}

	data := make(map[string]interface{})
	data["version"] = version.VERSION
	data["minVersion"] = GetMinVersion()
	data["hostCount"] = common.GetSyncMapLen(&file.GetDb().JsonDb.Hosts)
	data["clientCount"] = common.GetSyncMapLen(&file.GetDb().JsonDb.Clients)
	if cfg.Runtime.PublicVKey != "" { // remove public vkey
		data["clientCount"] = data["clientCount"].(int) - 1
	}

	dealClientData()

	c := 0
	var in, out int64
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if v.IsConnect {
			c++
		}
		clientIn := v.Flow.InletFlow - (v.InletFlow + v.ExportFlow)
		if clientIn < 0 {
			clientIn = 0
		}
		clientOut := v.Flow.ExportFlow - (v.InletFlow + v.ExportFlow)
		if clientOut < 0 {
			clientOut = 0
		}
		in += v.InletFlow + clientIn/2
		out += v.ExportFlow + clientOut/2
		return true
	})
	data["clientOnlineCount"] = c
	data["inletFlowCount"] = int(in)
	data["exportFlowCount"] = int(out)

	var tcpN, udpN, secretN, socks5N, p2pN, httpN int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		t := value.(*file.Tunnel)
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
	data["ipLimit"] = cfg.Runtime.IPLimit
	data["flowStoreInterval"] = intStringOrEmpty(cfg.Runtime.FlowStoreInterval)
	data["serverIp"] = common.GetServerIp(connection.P2pIp)
	data["serverIpv4"] = common.GetOutboundIPv4().String()
	data["serverIpv6"] = common.GetOutboundIPv6().String()
	data["p2pIp"] = connection.P2pIp
	data["p2pPort"] = connection.P2pPort
	data["p2pAddr"] = common.BuildAddress(common.GetServerIp(connection.P2pIp), strconv.Itoa(connection.P2pPort))
	data["logLevel"] = cfg.Log.Level
	data["upTime"] = common.GetRunTime()
	data["upSecs"] = common.GetRunSecs()
	data["startTime"] = common.GetStartTime()

	tcpCount := 0
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		tcpCount += int(value.(*file.Client).NowConn)
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

	if v, ok := ioSendRate.Load().(float64); ok {
		data["io_send"] = v
	}
	if v, ok := ioRecvRate.Load().(float64); ok {
		data["io_recv"] = v
	}

	// chart
	deciles := tool.ChartDeciles()
	for i, v := range deciles {
		data["sys"+strconv.Itoa(i+1)] = v
	}

	now := time.Now()
	cacheMu.Lock()
	dashboardCache = data
	lastRefresh = now
	lastFullRefresh = now
	cacheMu.Unlock()

	return cloneDashboardData(data)
}

func GetVersion() string {
	return version.VERSION
}

func GetMinVersion() string {
	return version.GetMinVersion(bridge.ServerSecureMode)
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
