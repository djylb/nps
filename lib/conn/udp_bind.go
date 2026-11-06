package conn

import (
	"fmt"
	"net"
	"strconv"

	"github.com/djylb/nps/lib/common"
)

func resolveUDPBindTarget(addr string) (*net.UDPAddr, string, string, bool, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, "", "", false, err
	}
	host := common.GetIpByAddr(addr)
	if host == "" {
		return udpAddr, "", "", false, nil
	}
	ip := common.NormalizeIP(udpAddr.IP)
	if ip == nil {
		ip = common.NormalizeIP(net.ParseIP(host))
	}
	if ip == nil {
		return nil, "", "", false, fmt.Errorf("invalid udp bind ip %q", host)
	}
	network := "udp6"
	if ip.To4() != nil {
		network = "udp4"
	}
	return udpAddr, network, net.JoinHostPort(ip.String(), strconv.Itoa(udpAddr.Port)), true, nil
}
