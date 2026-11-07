package connection

import (
	"fmt"
	"net"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
)

var pMux *portMux

// network holds the listener settings nps started with: listeners keep
// them until a restart, whatever a reload of the config says.
var network atomic.Pointer[servercfg.NetworkConfig]

// Network returns the listener settings nps started with.
func Network() servercfg.NetworkConfig {
	if n := network.Load(); n != nil {
		return *n
	}
	return servercfg.NetworkConfig{}
}

// Configure sets the listener settings without opening anything.
func Configure(n servercfg.NetworkConfig) {
	n.QUICALPNList = slices.Clone(n.QUICALPNList)
	network.Store(&n)
}

func InitConnectionService() {
	cfg := servercfg.Current()
	Configure(cfg.Network)
	n := Network()
	legacy.SetPingInterval(time.Duration(n.MuxPingInterval) * time.Second)

	if n.BridgePort != 0 && (n.HTTPProxyPort == n.BridgePort || n.HTTPSProxyPort == n.BridgePort || n.WebPort == n.BridgePort || n.BridgeTLSPort == n.BridgePort) {
		if n.BridgePort <= 0 || n.BridgePort > 65535 {
			logs.Error("Invalid bridge port %d", n.BridgePort)
			os.Exit(0)
		}
		var err error
		if pMux, err = newPortMux(n.BridgePort, cfg.Web.Host, cfg.Network.BridgeHost); err != nil {
			logs.Error("Port multiplexing on %d failed: %v", n.BridgePort, err)
			os.Exit(1)
		}
	}
}

func GetBridgeTcpListener() (net.Listener, error) {
	n := Network()
	logs.Info("server start, the bridge type is tcp, the bridge port is %d", n.BridgeTCPPort)
	if n.BridgeTCPPort <= 0 || n.BridgeTCPPort > 65535 {
		return nil, fmt.Errorf("invalid tcp bridge port %d", n.BridgeTCPPort)
	}
	if pMux != nil && n.BridgeTCPPort == n.BridgePort {
		return pMux.register(kindClient)
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(n.BridgeTCPIP), Port: n.BridgeTCPPort})
}

func GetBridgeTlsListener() (net.Listener, error) {
	n := Network()
	logs.Info("server start, the bridge type is tls, the bridge port is %d", n.BridgeTLSPort)
	if n.BridgeTLSPort <= 0 || n.BridgeTLSPort > 65535 {
		return nil, fmt.Errorf("invalid tls bridge port %d", n.BridgeTLSPort)
	}
	if pMux != nil && n.BridgeTLSPort == n.BridgePort {
		return pMux.register(kindClientTls)
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(n.BridgeTLSIP), Port: n.BridgeTLSPort})
}

func GetBridgeWsListener() (net.Listener, error) {
	n := Network()
	logs.Info("server start, the bridge type is ws, the bridge port is %d, the bridge path is %s", n.BridgeWSPort, n.BridgePath)
	if n.BridgeWSPort <= 0 || n.BridgeWSPort > 65535 {
		return nil, fmt.Errorf("invalid ws bridge port %d", n.BridgeWSPort)
	}
	if pMux != nil && n.BridgeWSPort == n.BridgePort {
		return pMux.register(kindClientWs)
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(n.BridgeWSIP), Port: n.BridgeWSPort})
}

func GetBridgeWssListener() (net.Listener, error) {
	n := Network()
	logs.Info("server start, the bridge type is wss, the bridge port is %d, the bridge path is %s", n.BridgeWSSPort, n.BridgePath)
	if n.BridgeWSSPort <= 0 || n.BridgeWSSPort > 65535 {
		return nil, fmt.Errorf("invalid wss bridge port %d", n.BridgeWSSPort)
	}
	if pMux != nil && n.BridgeWSSPort == n.BridgePort {
		return pMux.register(kindClientWss)
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(n.BridgeWSSIP), Port: n.BridgeWSSPort})
}

func GetHttpListener() (net.Listener, error) {
	n := Network()
	if pMux != nil && n.HTTPProxyPort == n.BridgePort {
		logs.Info("start http listener, port is %d", n.BridgePort)
		return pMux.register(kindHttp)
	}
	logs.Info("start http listener, port is %d", n.HTTPProxyPort)
	return getTcpListener(n.HTTPProxyIP, n.HTTPProxyPort)
}

func GetHttpsListener() (net.Listener, error) {
	n := Network()
	if pMux != nil && n.HTTPSProxyPort == n.BridgePort {
		logs.Info("start https listener, port is %d", n.BridgePort)
		return pMux.register(kindHttps)
	}
	logs.Info("start https listener, port is %d", n.HTTPSProxyPort)
	return getTcpListener(n.HTTPProxyIP, n.HTTPSProxyPort)
}

func GetWebManagerListener() (net.Listener, error) {
	n := Network()
	if pMux != nil && n.WebPort == n.BridgePort {
		logs.Info("Web management start, access port is %d", n.BridgePort)
		return pMux.register(kindManager)
	}
	logs.Info("web management start, access port is %d", n.WebPort)
	return getTcpListener(n.WebIP, n.WebPort)
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
