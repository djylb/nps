package connection

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/beego/beego"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/pmux"
)

var pMux *pmux.PortMux
var BridgeIp string
var BridgeTcpIp string
var BridgeKcpIp string
var BridgeQuicIp string
var BridgeTlsIp string
var BridgeWsIp string
var BridgeWssIp string
var BridgePort string
var BridgeTcpPort string
var BridgeKcpPort string
var BridgeQuicPort string
var BridgeTlsPort string
var BridgeWsPort string
var BridgeWssPort string
var BridgePath string
var HttpIp string
var HttpPort string
var HttpsPort string
var Http3Port string
var WebIp string
var WebPort string
var P2pIp string
var P2pPort string
var QuicAlpn []string
var QuicKeepAliveSec int
var QuicIdleTimeoutSec int
var QuicMaxStreams int64
var MuxPingIntervalSec int

func InitConnectionService() {
	BridgeIp = beego.AppConfig.DefaultString("bridge_ip", beego.AppConfig.String("bridge_tcp_ip"))
	BridgeTcpIp = beego.AppConfig.DefaultString("bridge_tcp_ip", BridgeIp)
	BridgeKcpIp = beego.AppConfig.DefaultString("bridge_kcp_ip", BridgeIp)
	BridgeQuicIp = beego.AppConfig.DefaultString("bridge_quic_ip", BridgeIp)
	BridgeTlsIp = beego.AppConfig.DefaultString("bridge_tls_ip", BridgeIp)
	BridgeWsIp = beego.AppConfig.DefaultString("bridge_ws_ip", BridgeIp)
	BridgeWssIp = beego.AppConfig.DefaultString("bridge_wss_ip", BridgeIp)
	BridgePort = beego.AppConfig.DefaultString("bridge_port", beego.AppConfig.String("bridge_tcp_port"))
	BridgeTcpPort = beego.AppConfig.DefaultString("bridge_tcp_port", BridgePort)
	BridgeKcpPort = beego.AppConfig.DefaultString("bridge_kcp_port", BridgePort)
	BridgeQuicPort = beego.AppConfig.String("bridge_quic_port")
	BridgeTlsPort = beego.AppConfig.DefaultString("bridge_tls_port", beego.AppConfig.String("tls_bridge_port"))
	BridgeWsPort = beego.AppConfig.String("bridge_ws_port")
	BridgeWssPort = beego.AppConfig.String("bridge_wss_port")
	BridgePath = beego.AppConfig.String("bridge_path")
	HttpIp = beego.AppConfig.String("http_proxy_ip")
	HttpPort = beego.AppConfig.String("http_proxy_port")
	HttpsPort = beego.AppConfig.String("https_proxy_port")
	Http3Port = beego.AppConfig.DefaultString("http3_proxy_port", HttpsPort)
	WebIp = beego.AppConfig.String("web_ip")
	WebPort = beego.AppConfig.String("web_port")
	P2pIp = beego.AppConfig.String("p2p_ip")
	P2pPort = beego.AppConfig.String("p2p_port")
	quicAlpnList := beego.AppConfig.DefaultString("quic_alpn", "nps")
	QuicAlpn = strings.Split(quicAlpnList, ",")
	QuicKeepAliveSec = beego.AppConfig.DefaultInt("quic_keep_alive_period", 10)
	QuicIdleTimeoutSec = beego.AppConfig.DefaultInt("quic_max_idle_timeout", 30)
	QuicMaxStreams = beego.AppConfig.DefaultInt64("quic_max_incoming_streams", 100000)
	MuxPingIntervalSec = beego.AppConfig.DefaultInt("mux_ping_interval", 5)
	mux.PingInterval = time.Duration(MuxPingIntervalSec) * time.Second

	if HttpPort == BridgePort || HttpsPort == BridgePort || WebPort == BridgePort || BridgeTlsPort == BridgePort {
		port, err := strconv.Atoi(BridgePort)
		if err != nil {
			logs.Error("%v", err)
			os.Exit(0)
		}
		pMux = pmux.NewPortMux(port, beego.AppConfig.String("web_host"), beego.AppConfig.String("bridge_host"))
	}
}

func GetBridgeTcpListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tcp, the bridge port is %s", BridgeTcpPort)
	var p int
	var err error
	if p, err = strconv.Atoi(BridgeTcpPort); err != nil {
		return nil, err
	}
	if pMux != nil && BridgeTcpPort == BridgePort {
		return pMux.GetClientListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTcpIp), Port: p})
}

func GetBridgeTlsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tls, the bridge port is %s", BridgeTlsPort)
	var p int
	var err error
	if p, err = strconv.Atoi(BridgeTlsPort); err != nil {
		return nil, err
	}
	if pMux != nil && BridgeTlsPort == BridgePort {
		return pMux.GetClientTlsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTlsIp), Port: p})
}

func GetBridgeWsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is ws, the bridge port is %s, the bridge path is %s", BridgeWsPort, BridgePath)
	var p int
	var err error
	if p, err = strconv.Atoi(BridgeWsPort); err != nil {
		return nil, err
	}
	if pMux != nil && BridgeWsPort == BridgePort {
		return pMux.GetClientWsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWsIp), Port: p})
}

func GetBridgeWssListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is wss, the bridge port is %s, the bridge path is %s", BridgeWssPort, BridgePath)
	var p int
	var err error
	if p, err = strconv.Atoi(BridgeWssPort); err != nil {
		return nil, err
	}
	if pMux != nil && BridgeWssPort == BridgePort {
		return pMux.GetClientWssListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWssIp), Port: p})
}

func GetHttpListener() (net.Listener, error) {
	if pMux != nil && HttpPort == BridgePort {
		logs.Info("start http listener, port is %s", BridgePort)
		return pMux.GetHttpListener(), nil
	}
	logs.Info("start http listener, port is %s", HttpPort)
	return getTcpListener(HttpIp, HttpPort)
}

func GetHttpsListener() (net.Listener, error) {
	if pMux != nil && HttpsPort == BridgePort {
		logs.Info("start https listener, port is %s", BridgePort)
		return pMux.GetHttpsListener(), nil
	}
	logs.Info("start https listener, port is %s", HttpsPort)
	return getTcpListener(HttpIp, HttpsPort)
}

func GetWebManagerListener() (net.Listener, error) {
	if pMux != nil && WebPort == BridgePort {
		logs.Info("Web management start, access port is %s", BridgePort)
		return pMux.GetManagerListener(), nil
	}
	logs.Info("web management start, access port is %s", WebPort)
	return getTcpListener(WebIp, WebPort)
}

func getTcpListener(ip, p string) (net.Listener, error) {
	port, err := strconv.Atoi(p)
	if err != nil {
		logs.Error("%v", err)
		os.Exit(0)
	}
	if ip == "" {
		ip = "0.0.0.0"
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(ip), Port: port})
}
