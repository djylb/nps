package common

import (
	"context"
	"net"
	"strings"
)

var customDnsAddr string

func SetCustomDNS(dnsAddr string) {
	if dnsAddr == "" {
		return
	}
	colonCount := strings.Count(dnsAddr, ":")
	if colonCount == 0 {
		dnsAddr += ":53"
	} else if colonCount > 1 && !strings.Contains(dnsAddr, "]:") {
		if strings.Contains(dnsAddr, "]") {
			dnsAddr += ":53"
		} else {
			dnsAddr = "[" + dnsAddr + "]:53"
		}
	}

	customDnsAddr = dnsAddr

	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.Dial(network, dnsAddr)
		},
	}
}

func GetCustomDNS() string {
	if customDnsAddr != "" {
		return customDnsAddr
	}
	return "8.8.8.8:53"
}
