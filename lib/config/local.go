package config

// LocalServer is a point-to-point access listener of a visitor npc: a
// [secret...] section, the -password command line or a launch payload. It
// reaches the secret tunnel of another client whose password it gives,
// directly (hole punching) or relayed by nps as ConnMode says.
type LocalServer struct {
	Type       string // LocalTCP, LocalUDP, LocalTCPUDP, LocalMixed, LocalTransparent
	ConnMode   string // ConnAuto, ConnRelay, ConnDirect
	Port       int
	Ip         string // listen address; empty for all
	Password   string
	Target     string // the destination the visitor names, within the tunnel's destination list; empty for the tunnel's default target
	LocalProxy bool
	Auth       map[string]string // proxy accounts of a mixed, socks5 or http listener, none for no authentication
}

// Listener types of a LocalServer.
const (
	LocalTCP         = "tcp"
	LocalUDP         = "udp"
	LocalTCPUDP      = "tcp_udp"
	LocalMixed       = "mixed"       // HTTP and SOCKS5 proxy
	LocalSOCKS5      = "socks5"      // the SOCKS5 half of mixed alone
	LocalHTTP        = "http"        // the HTTP half of mixed alone
	LocalTransparent = "transparent" // transparent proxy (Linux)
)

// Connection modes of a LocalServer, as the later major version names them.
const (
	ConnAuto   = "auto"   // direct when hole punching succeeds, else relayed by nps
	ConnRelay  = "relay"  // always relayed by nps
	ConnDirect = "direct" // only direct; fails when hole punching does
)

// LocalTypes and ConnModes are the accepted values.
var (
	LocalTypes = []string{LocalTCP, LocalUDP, LocalTCPUDP, LocalMixed, LocalSOCKS5, LocalHTTP, LocalTransparent}
	ConnModes  = []string{ConnAuto, ConnRelay, ConnDirect}
)

// IsProxyType reports whether a listener type is a proxy that reads the
// destination from each connection: mixed, socks5 and http, and transparent.
func IsProxyType(t string) bool {
	return IsAccountType(t) || t == LocalTransparent
}

// IsAccountType reports whether a listener type is a mixed, socks5 or http
// proxy, the types that take proxy accounts.
func IsAccountType(t string) bool {
	return t == LocalMixed || t == LocalSOCKS5 || t == LocalHTTP
}
