package connection

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/pmux"
	"github.com/djylb/nps/lib/servercfg"
)

var pMux *pmux.PortMux
var BridgeIp string
var BridgeHost string
var BridgeTcpIp string
var BridgeKcpIp string
var BridgeQuicIp string
var BridgeTlsIp string
var BridgeWsIp string
var BridgeWssIp string
var BridgePort int
var BridgeTcpPort int
var BridgeKcpPort int
var BridgeQuicPort int
var BridgeTlsPort int
var BridgeWsPort int
var BridgeWssPort int
var BridgePath string
var BridgeTrustedIps string
var BridgeRealIpHeader string
var HttpIp string
var HttpPort int
var HttpsPort int
var Http3Port int
var WebIp string
var WebPort int
var P2pIp string
var P2pPort int
var QuicAlpn []string
var QuicKeepAliveSec int
var QuicIdleTimeoutSec int
var QuicMaxStreams int64
var MuxPingIntervalSec int

func InitConnectionService() {
	cfg := servercfg.Current()
	BridgeIp = cfg.Network.BridgeIP
	BridgeHost = cfg.Network.BridgeHost
	BridgeTcpIp = cfg.Network.BridgeTCPIP
	BridgeKcpIp = cfg.Network.BridgeKCPIP
	BridgeQuicIp = cfg.Network.BridgeQUICIP
	BridgeTlsIp = cfg.Network.BridgeTLSIP
	BridgeWsIp = cfg.Network.BridgeWSIP
	BridgeWssIp = cfg.Network.BridgeWSSIP
	BridgePort = cfg.Network.BridgePort
	BridgeTcpPort = cfg.Network.BridgeTCPPort
	BridgeKcpPort = cfg.Network.BridgeKCPPort
	BridgeQuicPort = cfg.Network.BridgeQUICPort
	BridgeTlsPort = cfg.Network.BridgeTLSPort
	BridgeWsPort = cfg.Network.BridgeWSPort
	BridgeWssPort = cfg.Network.BridgeWSSPort
	BridgePath = cfg.Network.BridgePath
	BridgeTrustedIps = cfg.Network.BridgeTrustedIPs
	BridgeRealIpHeader = cfg.Network.BridgeRealIPHeader
	HttpIp = cfg.Network.HTTPProxyIP
	HttpPort = cfg.Network.HTTPProxyPort
	HttpsPort = cfg.Network.HTTPSProxyPort
	Http3Port = cfg.Network.HTTP3ProxyPort
	WebIp = cfg.Network.WebIP
	WebPort = cfg.Network.WebPort
	P2pIp = cfg.Network.P2PIP
	P2pPort = cfg.Network.P2PPort
	QuicAlpn = append([]string(nil), cfg.Network.QUICALPNList...)
	QuicKeepAliveSec = cfg.Network.QUICKeepAlivePeriod
	QuicIdleTimeoutSec = cfg.Network.QUICMaxIdleTimeout
	QuicMaxStreams = cfg.Network.QUICMaxIncomingStreams
	MuxPingIntervalSec = cfg.Network.MuxPingInterval
	mux.PingInterval = time.Duration(MuxPingIntervalSec) * time.Second

	if BridgePort != 0 && (HttpPort == BridgePort || HttpsPort == BridgePort || WebPort == BridgePort || BridgeTlsPort == BridgePort) {
		if BridgePort <= 0 || BridgePort > 65535 {
			logs.Error("Invalid bridge port %d", BridgePort)
			os.Exit(0)
		}
		var err error
		if pMux, err = pmux.NewPortMux(BridgePort, cfg.Web.Host, cfg.Network.BridgeHost); err != nil {
			logs.Error("Port multiplexing on %d failed: %v", BridgePort, err)
			os.Exit(1)
		}
	}
}

func GetBridgeTcpListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tcp, the bridge port is %d", BridgeTcpPort)
	if BridgeTcpPort <= 0 || BridgeTcpPort > 65535 {
		return nil, fmt.Errorf("invalid tcp bridge port %d", BridgeTcpPort)
	}
	if pMux != nil && BridgeTcpPort == BridgePort {
		return pMux.GetClientListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTcpIp), Port: BridgeTcpPort})
}

func GetBridgeTlsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tls, the bridge port is %d", BridgeTlsPort)
	if BridgeTlsPort <= 0 || BridgeTlsPort > 65535 {
		return nil, fmt.Errorf("invalid tls bridge port %d", BridgeTlsPort)
	}
	if pMux != nil && BridgeTlsPort == BridgePort {
		return pMux.GetClientTlsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTlsIp), Port: BridgeTlsPort})
}

func GetBridgeWsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is ws, the bridge port is %d, the bridge path is %s", BridgeWsPort, BridgePath)
	if BridgeWsPort <= 0 || BridgeWsPort > 65535 {
		return nil, fmt.Errorf("invalid ws bridge port %d", BridgeWsPort)
	}
	if pMux != nil && BridgeWsPort == BridgePort {
		return pMux.GetClientWsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWsIp), Port: BridgeWsPort})
}

func GetBridgeWssListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is wss, the bridge port is %d, the bridge path is %s", BridgeWssPort, BridgePath)
	if BridgeWssPort <= 0 || BridgeWssPort > 65535 {
		return nil, fmt.Errorf("invalid wss bridge port %d", BridgeWssPort)
	}
	if pMux != nil && BridgeWssPort == BridgePort {
		return pMux.GetClientWssListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWssIp), Port: BridgeWssPort})
}

func GetHttpListener() (net.Listener, error) {
	if pMux != nil && HttpPort == BridgePort {
		logs.Info("start http listener, port is %d", BridgePort)
		return pMux.GetHttpListener(), nil
	}
	logs.Info("start http listener, port is %d", HttpPort)
	return getTcpListener(HttpIp, HttpPort)
}

func GetHttpsListener() (net.Listener, error) {
	if pMux != nil && HttpsPort == BridgePort {
		logs.Info("start https listener, port is %d", BridgePort)
		return pMux.GetHttpsListener(), nil
	}
	logs.Info("start https listener, port is %d", HttpsPort)
	return getTcpListener(HttpIp, HttpsPort)
}

func GetWebManagerListener() (net.Listener, error) {
	if pMux != nil && WebPort == BridgePort {
		logs.Info("Web management start, access port is %d", BridgePort)
		return pMux.GetManagerListener(), nil
	}
	logs.Info("web management start, access port is %d", WebPort)
	return getTcpListener(WebIp, WebPort)
}

func getTcpListener(ip string, port int) (net.Listener, error) {
	if port <= 0 || port > 65535 {
		logs.Error("invalid tcp port %d", port)
		os.Exit(0)
	}
	if ip == "" {
		ip = "0.0.0.0"
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(ip), Port: port})
}
