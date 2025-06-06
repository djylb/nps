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
		return s.DealClient(c, s.task.Client, addr, nil, common.CONN_TCP, nil, []*file.Flow{s.task.Flow, s.task.Client.Flow}, s.task.Target.ProxyProtocol, s.task.Target.LocalProxy, s.task)
	}
}
