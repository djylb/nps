package bridge

import (
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/model"
)

// CoreRecord is what the new protocol entry knows of client c; ok is false
// for a client it does not admit.
func CoreRecord(c *model.Client) (coreapi.ClientRecord, bool) {
	public, visitor := c.KeyKind == model.KeyPublic, c.KeyKind == model.KeyVisitor
	if c.Id <= 0 || !public && !visitor && c.KeyKind != model.KeyControl {
		return coreapi.ClientRecord{}, false
	}
	return coreapi.ClientRecord{ID: c.Id, VKey: c.VerifyKey, Enabled: c.Status, ConfigOnly: public, VisitorOnly: visitor, Crypt: public || visitor || c.Cnf != nil && c.Cnf.Crypt, Compress: c.Cnf.CompressList(), SchedulerDown: c.Cnf.SchedulerDownName(), SchedulerUp: c.Cnf.SchedulerUpName(), Shaping: c.Cnf != nil && c.Cnf.Shaping}, true
}
