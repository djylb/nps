package main

import (
	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
)

// coreStore reads the stored clients for the new protocol entry.
type coreStore struct{ db *file.DbUtils }

func coreRecord(c *model.Client) (coreapi.ClientRecord, bool) {
	if c.Id <= 0 || c.NoStore || c.KeyKind != model.KeyControl {
		return coreapi.ClientRecord{}, false
	}
	return coreapi.ClientRecord{ID: c.Id, VKey: c.VerifyKey, Enabled: c.Status, Crypt: c.Cnf != nil && c.Cnf.Crypt}, true
}

// RangeClients implements session.Store.
func (s coreStore) RangeClients(fn func(coreapi.ClientRecord) bool) {
	s.db.JsonDb.Clients.Range(func(_, v any) bool {
		if r, ok := coreRecord(v.(*model.Client)); ok {
			return fn(r)
		}
		return true
	})
}

// newCoreEntry builds the new protocol entry, nil while new_protocol is off,
// and keeps it current with the store.
func newCoreEntry(db *file.DbUtils, cfg *servercfg.Snapshot) (coreapi.Entry, error) {
	if !cfg.Network.NewProtocol {
		return nil, nil
	}
	a, err := session.NewAcceptor(coreStore{db})
	if err != nil {
		return nil, err
	}
	db.OnClientChanged(func(c *model.Client, deleted bool) {
		if r, ok := coreRecord(c); ok && !deleted {
			a.ClientChanged(r)
		} else {
			a.ClientRemoved(c.Id)
		}
	})
	return a, nil
}
