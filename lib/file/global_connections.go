package file

import "github.com/djylb/nps/lib/rate"

// SetGlobalMaxConn updates admission without disconnecting existing transfers.
func SetGlobalMaxConn(limit int) { rate.RootGroup().SetMaxConns(int64(limit)) }

// GlobalConnections reports active forwarding slots and their configured limit.
func GlobalConnections() (active, limit int64) {
	g := rate.RootGroup()
	return g.Conns(), g.MaxConns()
}
