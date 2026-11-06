package client

import (
	"time"

	"github.com/djylb/nps/bridge/legacy"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/healthcheck"
)

// healthWriteTimeout bounds writing one report; a stuck signal connection is
// closed and the client reconnects.
const healthWriteTimeout = 10 * time.Second

// legacyHealthSink writes reports on a signal connection of the legacy protocol.
type legacyHealthSink struct{ signal *conn.Conn }

func (s legacyHealthSink) ReportHealth(rep healthcheck.Report) error {
	_ = s.signal.SetWriteDeadline(time.Now().Add(healthWriteTimeout))
	err := legacy.WriteGroupHealth(s.signal, rep)
	_ = s.signal.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = s.signal.Close()
	}
	return err
}
