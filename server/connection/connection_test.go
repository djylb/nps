package connection

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/servercfg"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nps-test.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func getAvailablePort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("alloc port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func TestInitConnectionServiceLoadsConfig(t *testing.T) {
	configPath := writeTestConfig(t, `
bridge_ip = 127.0.0.2
bridge_tcp_ip = 127.0.0.3
bridge_kcp_ip = 127.0.0.4
bridge_quic_ip = 127.0.0.5
bridge_tls_ip = 127.0.0.6
bridge_ws_ip = 127.0.0.7
bridge_wss_ip = 127.0.0.8
bridge_port = 18080
bridge_tcp_port = 18081
bridge_kcp_port = 18082
bridge_quic_port = 18083
bridge_tls_port = 18084
bridge_ws_port = 18085
bridge_wss_port = 18086
bridge_path = /bridge
bridge_trusted_ips = 127.0.0.1
bridge_real_ip_header = X-Real-IP
http_proxy_ip = 127.0.0.9
http_proxy_port = 19080
https_proxy_port = 19443
http3_proxy_port = 19444
web_ip = 127.0.0.10
web_port = 18000
p2p_ip = 127.0.0.11
p2p_port = 17000
quic_alpn = nps,test
quic_keep_alive_period = 12
quic_max_idle_timeout = 34
quic_max_incoming_streams = 999
mux_ping_interval = 8
`)

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}
	pMux = nil

	InitConnectionService()

	if BridgeIp != "127.0.0.2" || BridgeTcpIp != "127.0.0.3" || BridgeTlsIp != "127.0.0.6" {
		t.Fatalf("bridge ip fields not loaded correctly")
	}
	if BridgePath != "/bridge" || BridgeTrustedIps != "127.0.0.1" || BridgeRealIpHeader != "X-Real-IP" {
		t.Fatalf("bridge path/trusted fields not loaded correctly")
	}
	if BridgePort != 18080 || BridgeTcpPort != 18081 || BridgeWssPort != 18086 {
		t.Fatalf("bridge ports not loaded correctly")
	}
	if HttpIp != "127.0.0.9" || HttpPort != 19080 || HttpsPort != 19443 || Http3Port != 19444 {
		t.Fatalf("http settings not loaded correctly")
	}
	if WebIp != "127.0.0.10" || WebPort != 18000 || P2pIp != "127.0.0.11" || P2pPort != 17000 {
		t.Fatalf("web/p2p settings not loaded correctly")
	}
	if len(QuicAlpn) != 2 || QuicAlpn[0] != "nps" || QuicAlpn[1] != "test" {
		t.Fatalf("quic alpn not split correctly: %#v", QuicAlpn)
	}
	if QuicKeepAliveSec != 12 || QuicIdleTimeoutSec != 34 || QuicMaxStreams != 999 {
		t.Fatalf("quic values not loaded correctly")
	}
	if MuxPingIntervalSec != 8 || mux.PingInterval != 8*time.Second {
		t.Fatalf("mux ping interval not loaded correctly")
	}
}

func TestGetBridgeListenersInvalidPort(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		call func() (net.Listener, error)
	}{
		{"tcp", func() { BridgeTcpPort = 0 }, GetBridgeTcpListener},
		{"tls", func() { BridgeTlsPort = 70000 }, GetBridgeTlsListener},
		{"ws", func() { BridgeWsPort = -1 }, GetBridgeWsListener},
		{"wss", func() { BridgeWssPort = 0 }, GetBridgeWssListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pMux = nil
			tt.set()
			l, err := tt.call()
			if err == nil || l != nil {
				t.Fatalf("expected invalid port error, got listener=%v err=%v", l, err)
			}
		})
	}
}

func TestGetBridgeTcpListenerValid(t *testing.T) {
	pMux = nil
	BridgeTcpIp = "127.0.0.1"
	BridgeTcpPort = getAvailablePort(t)

	l, err := GetBridgeTcpListener()
	if err != nil {
		t.Fatalf("GetBridgeTcpListener() error = %v", err)
	}
	_ = l.Close()
}

func TestGetBridgeTlsWsWssListenersValid(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		call func() (net.Listener, error)
	}{
		{"tls", func() {
			BridgeTlsIp = "127.0.0.1"
			BridgeTlsPort = getAvailablePort(t)
		}, GetBridgeTlsListener},
		{"ws", func() {
			BridgeWsIp = "127.0.0.1"
			BridgeWsPort = getAvailablePort(t)
			BridgePath = "/ws"
		}, GetBridgeWsListener},
		{"wss", func() {
			BridgeWssIp = "127.0.0.1"
			BridgeWssPort = getAvailablePort(t)
			BridgePath = "/wss"
		}, GetBridgeWssListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pMux = nil
			tt.set()
			l, err := tt.call()
			if err != nil {
				t.Fatalf("%s listener error = %v", tt.name, err)
			}
			_ = l.Close()
		})
	}
}
