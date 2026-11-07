package connection

import (
	"fmt"
	"net"
	"slices"
	"sync/atomic"

	"github.com/djylb/nps/lib/obs"
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

// InitConnectionService loads the listener settings and, when several
// listeners share the bridge port, opens the port multiplexer on it.
func InitConnectionService() error {
	cfg := servercfg.Current()
	Configure(cfg.Network)
	n := Network()

	if n.BridgePort != 0 && (n.HTTPProxyPort == n.BridgePort || n.HTTPSProxyPort == n.BridgePort || n.WebPort == n.BridgePort || n.BridgeTLSPort == n.BridgePort) {
		if !validPort(n.BridgePort) {
			return fmt.Errorf("invalid bridge port %d", n.BridgePort)
		}
		var err error
		if pMux, err = newPortMux(n.BridgePort, cfg.Web.Host, cfg.Network.BridgeHost); err != nil {
			return fmt.Errorf("port multiplexing on %d: %w", n.BridgePort, err)
		}
	}
	return nil
}

func (k Kind) String() string {
	switch k {
	case KindBridgeTCP:
		return "tcp bridge"
	case KindBridgeTLS:
		return "tls bridge"
	case KindBridgeWS:
		return "ws bridge"
	case KindBridgeWSS:
		return "wss bridge"
	case KindHTTP:
		return "http proxy"
	case KindHTTPS:
		return "https proxy"
	case KindWeb:
		return "web manager"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// address returns the configured ip and port of one listener.
func address(n *servercfg.NetworkConfig, k Kind) (string, int, bool) {
	switch k {
	case KindBridgeTCP:
		return n.BridgeTCPIP, n.BridgeTCPPort, true
	case KindBridgeTLS:
		return n.BridgeTLSIP, n.BridgeTLSPort, true
	case KindBridgeWS:
		return n.BridgeWSIP, n.BridgeWSPort, true
	case KindBridgeWSS:
		return n.BridgeWSSIP, n.BridgeWSSPort, true
	case KindHTTP:
		return n.HTTPProxyIP, n.HTTPProxyPort, true
	case KindHTTPS:
		return n.HTTPProxyIP, n.HTTPSProxyPort, true
	case KindWeb:
		return n.WebIP, n.WebPort, true
	}
	return "", 0, false
}

// Listen opens the listener of kind k on its configured address, or takes
// its share of the port multiplexer when it sits on the bridge port.
func Listen(k Kind) (net.Listener, error) {
	n := Network()
	ip, port, ok := address(&n, k)
	if !ok {
		return nil, fmt.Errorf("unknown listener %v", k)
	}
	if !validPort(port) {
		return nil, fmt.Errorf("invalid %v port %d", k, port)
	}
	log.Info("listener starting", "kind", k, "port", port)
	if pMux != nil && port == n.BridgePort {
		return pMux.register(k)
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(ip), Port: port})
}

func validPort(port int) bool { return port > 0 && port <= 65535 }

var log = obs.Module("server")
