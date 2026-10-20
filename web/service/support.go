package service

import (
	"crypto/subtle"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
)

type BridgeEndpoint struct {
	Type string
	Addr string
	IP   string
	Port string
}

func UniqueStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for i := len(values) - 1; i >= 0; i-- {
		if _, ok := seen[values[i]]; ok {
			continue
		}
		seen[values[i]] = struct{}{}
		result = append(result, values[i])
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func ValidAuthKey(configKey, md5Key string, timestamp int, nowUnix int64) bool {
	if configKey == "" || md5Key == "" {
		return false
	}
	if math.Abs(float64(nowUnix-int64(timestamp))) > 20 {
		return false
	}
	expected := crypt.Md5(configKey + strconv.Itoa(timestamp))
	if len(expected) != len(md5Key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(md5Key)) == 1
}

func BestBridge(cfg *servercfg.Snapshot, host string) BridgeEndpoint {
	if cfg == nil {
		cfg = servercfg.Current()
	}
	bridgeIP := common.GetIpByAddr(defaultString(cfg.Bridge.Addr, host))
	if strings.IndexByte(bridgeIP, ':') >= 0 && (!strings.HasPrefix(bridgeIP, "[") || !strings.HasSuffix(bridgeIP, "]")) {
		bridgeIP = "[" + bridgeIP + "]"
	}

	result := BridgeEndpoint{
		Type: cfg.Bridge.PrimaryType,
		IP:   bridgeIP,
		Port: strconv.Itoa(connection.BridgePort),
	}
	result.Addr = result.IP + ":" + result.Port

	switch {
	case cfg.Bridge.Display.TLS.Show:
		result.Type = "tls"
		result.Port = defaultString(cfg.Bridge.Display.TLS.Port, strconv.Itoa(connection.BridgeTlsPort))
		result.IP = defaultString(cfg.Bridge.Display.TLS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.QUIC.Show:
		result.Type = "quic"
		result.Port = defaultString(cfg.Bridge.Display.QUIC.Port, strconv.Itoa(connection.BridgeQuicPort))
		result.IP = defaultString(cfg.Bridge.Display.QUIC.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
		quicALPN := defaultString(cfg.Bridge.Display.QUIC.ALPN, defaultQuicALPN())
		if quicALPN != "" && quicALPN != "nps" {
			result.Addr += "/" + quicALPN
		}
	case cfg.Bridge.Display.WSS.Show:
		result.Type = "wss"
		result.Port = defaultString(cfg.Bridge.Display.WSS.Port, strconv.Itoa(connection.BridgeWssPort))
		result.IP = defaultString(cfg.Bridge.Display.WSS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port + cfg.Bridge.Display.Path
	case cfg.Bridge.Display.TCP.Show:
		result.Type = "tcp"
		result.Port = defaultString(cfg.Bridge.Display.TCP.Port, strconv.Itoa(connection.BridgeTcpPort))
		result.IP = defaultString(cfg.Bridge.Display.TCP.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.KCP.Show:
		result.Type = "kcp"
		result.Port = defaultString(cfg.Bridge.Display.KCP.Port, strconv.Itoa(connection.BridgeKcpPort))
		result.IP = defaultString(cfg.Bridge.Display.KCP.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.WS.Show:
		result.Type = "ws"
		result.Port = defaultString(cfg.Bridge.Display.WS.Port, strconv.Itoa(connection.BridgeWsPort))
		result.IP = defaultString(cfg.Bridge.Display.WS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port + cfg.Bridge.Display.Path
	}

	return result
}

func ClearClientStatus(client *file.Client, name string) {
	clearClientStatus(client, name)
}

func changeTunnelStatus(id int, name, action string) error {
	tunnel, err := DefaultBackend().repo().GetTunnel(id)
	if err != nil {
		return err
	}

	switch strings.TrimSpace(name) {
	case "http":
		if err := applyBoolAction(&tunnel.HttpProxy, action); err != nil {
			return err
		}
	case "socks5":
		if err := applyBoolAction(&tunnel.Socks5Proxy, action); err != nil {
			return err
		}
	case "flow":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		tunnel.Flow.ResetTraffic()
	case "flow_limit":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		tunnel.Flow.SetFlowLimit(0)
	case "time_limit":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		tunnel.Flow.SetTimeLimit(common.GetTimeNoErrByStr(""))
	default:
		return fmt.Errorf("unknown name: %q", name)
	}

	return DefaultBackend().repo().SaveTunnel(tunnel)
}

func changeHostStatus(id int, name, action string) error {
	host, err := DefaultBackend().repo().GetHost(id)
	if err != nil {
		return err
	}

	switch strings.TrimSpace(name) {
	case "flow":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		host.Flow.ResetTraffic()
	case "flow_limit":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		host.Flow.SetFlowLimit(0)
	case "time_limit":
		if normalizeAction(action) != "clear" {
			return fmt.Errorf("unsupported action %q for %s", action, name)
		}
		host.Flow.SetTimeLimit(common.GetTimeNoErrByStr(""))
	case "auto_ssl":
		if err := applyBoolAction(&host.AutoSSL, action); err != nil {
			return err
		}
	case "https_just_proxy":
		if err := applyBoolAction(&host.HttpsJustProxy, action); err != nil {
			return err
		}
	case "tls_offload":
		if err := applyBoolAction(&host.TlsOffload, action); err != nil {
			return err
		}
	case "auto_https":
		if err := applyBoolAction(&host.AutoHttps, action); err != nil {
			return err
		}
	case "auto_cors":
		if err := applyBoolAction(&host.AutoCORS, action); err != nil {
			return err
		}
	case "compat_mode":
		if err := applyBoolAction(&host.CompatMode, action); err != nil {
			return err
		}
	case "target_is_https":
		if err := applyBoolAction(&host.TargetIsHttps, action); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown name: %q", name)
	}

	host.CertType = common.GetCertType(host.CertFile)
	host.CertHash = crypt.FNV1a64(host.CertType, host.CertFile, host.KeyFile)
	return DefaultBackend().repo().SaveHost(host, "")
}

func clearClientStatusByID(id int, name string) error {
	if id == 0 {
		DefaultBackend().repo().RangeClients(func(client *file.Client) bool {
			clearClientStatus(client, name)
			return true
		})
		if strings.TrimSpace(name) == "flow" {
			// clearing every client's traffic clears the server's total
			rate.ResetGlobalTraffic()
			if err := DefaultBackend().repo().PersistGlobal(); err != nil {
				return err
			}
		}
		return DefaultBackend().repo().PersistClients()
	}

	client, err := DefaultBackend().repo().GetClient(id)
	if err != nil {
		return err
	}
	clearClientStatus(client, name)
	return DefaultBackend().repo().SaveClient(client)
}

func clearClientStatus(client *file.Client, name string) {
	switch strings.TrimSpace(name) {
	case "flow":
		client.Flow.ResetTraffic()
		client.ExportFlow = 0
		client.InletFlow = 0
		DefaultBackend().repo().RangeHosts(func(host *file.Host) bool {
			if host.Client.Id == client.Id {
				host.Flow.ResetTraffic()
			}
			return true
		})
		DefaultBackend().repo().RangeTunnels(func(tunnel *file.Tunnel) bool {
			if tunnel.Client.Id == client.Id {
				tunnel.Flow.ResetTraffic()
			}
			return true
		})
	case "flow_limit":
		client.Flow.SetFlowLimit(0)
	case "time_limit":
		client.Flow.SetTimeLimit(common.GetTimeNoErrByStr(""))
	case "rate_limit":
		client.RateLimit = 0
	case "conn_limit":
		client.SetMaxConn(0)
	case "tunnel_limit":
		client.MaxTunnelNum = 0
	}

	var limit int64
	if client.RateLimit > 0 {
		limit = int64(client.RateLimit) * 1024
	}
	if client.Rate == nil {
		client.Rate = rate.NewRate(limit)
		client.Rate.Start()
	} else if client.Rate.Limit() != limit {
		client.Rate.ResetLimit(limit)
	} else {
		client.Rate.Start()
	}
}

func normalizeAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

func applyBoolAction(dst *bool, action string) error {
	switch normalizeAction(action) {
	case "start", "true", "on":
		*dst = true
	case "stop", "false", "off":
		*dst = false
	case "clear", "turn", "switch":
		*dst = !*dst
	default:
		return fmt.Errorf("unknown action: %q", action)
	}
	return nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultQuicALPN() string {
	if len(connection.QuicAlpn) > 0 {
		return connection.QuicAlpn[0]
	}
	return "nps"
}
