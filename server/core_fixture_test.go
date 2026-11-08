package server

import (
	"testing"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
)

// stubEntry is the entry of tests that run no bridge ports.
type stubEntry struct{ coreapi.Entry }

func (stubEntry) Kick(int, bool)                                       {}
func (stubEntry) RevokeLinks(func(tunnel, visitor, provider int) bool) {}

type dbClients struct{ db *file.DbUtils }

func (s dbClients) RangeClients(fn func(coreapi.ClientRecord) bool) {
	s.db.JsonDb.Clients.Range(func(_, v any) bool {
		c := v.(*model.Client)
		return fn(coreapi.ClientRecord{ID: c.Id, VKey: c.VerifyKey, Enabled: c.Status, Crypt: c.Cnf.Crypt, Compress: c.Cnf.CompressList()})
	})
}

// realEntry is the entry of tests that run the bridge ports.
func realEntry(t *testing.T, db *file.DbUtils) coreapi.Entry {
	t.Helper()
	a, err := session.NewAcceptor(dbClients{db}, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
