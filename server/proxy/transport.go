//go:build !windows
// +build !windows

package proxy

import (
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/transport"
)

func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	if addr, err := transport.GetAddress(c.Conn); err != nil {
		return err
	} else {
		return s.DealClient(c, s.Task.Client, addr, nil, common.CONN_TCP, nil, []*file.Flow{s.Task.Flow, s.Task.Client.Flow}, s.Task.Target.ProxyProtocol, s.Task.Target.LocalProxy, s.Task)
	}
}
