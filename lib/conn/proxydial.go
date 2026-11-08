package conn

import (
	"context"
	"net"
	"time"

	"github.com/djylb/netx"
	"github.com/djylb/netx/proxydial"
	"github.com/djylb/nps/lib/netaddr"
)

// DialProxy dials server directly, through proxyUrl, or through the proxy
// configured in ALL_PROXY and NO_PROXY. proxyUrl may use the http, https,
// socks5, socks5h, socks4 and socks4a schemes, or be a bare host:port, which
// is taken as an http proxy. The whole dial, including any proxy handshake,
// is bounded by timeout.
func DialProxy(proxyUrl, server string, timeout time.Duration, localIP string) (net.Conn, error) {
	forward := &net.Dialer{Timeout: timeout, LocalAddr: netaddr.BuildTCPBindAddr(localIP)}
	var d netx.ContextDialer
	if proxyUrl == "" {
		var err error
		if d, err = proxydial.FromEnvironment(forward, nil); err != nil {
			return nil, err
		}
	} else {
		u, err := proxydial.ParseURL(proxyUrl)
		if err != nil {
			return nil, err
		}
		if d, err = proxydial.FromURL(u, forward, nil); err != nil {
			return nil, err
		}
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return d.DialContext(ctx, "tcp", server)
}
