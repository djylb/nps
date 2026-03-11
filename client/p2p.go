package client

import (
	"context"
	"net"

	libp2p "github.com/djylb/nps/lib/p2p"
)

func handleP2PUdp(
	pCtx context.Context,
	localAddr, rAddr, md5Password, sendRole, sendMode, sendData string,
) (c net.PacketConn, remoteAddress, localAddress, role, mode, data string, err error) {
	return libp2p.HandleUDP(pCtx, localAddr, rAddr, md5Password, sendRole, sendMode, sendData)
}
