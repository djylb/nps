package common

const (
	ConnDataSeq = "*#*" // Separator
	ConnAll     = "all"
	ConnTCP     = "tcp"
	ConnUDP     = "udp"
	ConnTLS     = "tls"
	ConnQUIC    = "quic"
	ConnWeb     = "web"
	ConnWS      = "ws"
	ConnWSS     = "wss"

	TOTPSeq = "totp:" // TOTP Separator

	UnauthorizedBytes = "HTTP/1.1 401 Unauthorized\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"WWW-Authenticate: Basic realm=\"easyProxy\"\r\n" +
		"\r\n" +
		"401 Unauthorized"

	ProxyAuthRequiredBytes = "HTTP/1.1 407 Proxy Authentication Required\r\n" +
		"Proxy-Authenticate: Basic realm=\"Proxy\"\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n" +
		"\r\n"

	ConnectionFailBytes = "HTTP/1.1 404 Not Found\r\n" +
		"\r\n"

	IPv4DNS = "8.8.8.8:53"
	IPv6DNS = "[2400:3200::1]:53"
)

var DefaultPort = map[string]string{
	"tcp":  "8024",
	"kcp":  "8024",
	"tls":  "8025",
	"quic": "8025",
	"ws":   "80",
	"wss":  "443",
}
