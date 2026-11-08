package main

import (
	"context"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/internal/coreproto/session"
	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/nps/lib/servercfg"
)

// coreStore reads the stored clients for the new protocol entry.
type coreStore struct{ db *file.DbUtils }

func coreRecord(c *model.Client) (coreapi.ClientRecord, bool) {
	public := c.KeyKind == model.KeyPublic
	if c.Id <= 0 || !public && c.KeyKind != model.KeyControl {
		return coreapi.ClientRecord{}, false
	}
	return coreapi.ClientRecord{ID: c.Id, VKey: c.VerifyKey, Enabled: c.Status, ConfigOnly: public, Crypt: public || c.Cnf != nil && c.Cnf.Crypt, Compress: c.Cnf.CompressList(), SchedulerDown: c.Cnf.SchedulerDownName(), Shaping: c.Cnf != nil && c.Cnf.Shaping}, true
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
	resumeS := cfg.Runtime.SessionResumeTimeout
	src, err := newShapingSource(cfg)
	if err != nil {
		return nil, err
	}
	var from func(context.Context, func([]byte))
	if src != nil {
		from = src.run
	}
	a, err := session.NewAcceptor(coreStore{db}, session.Options{
		ShapingSource: from,
		// Told to npc only when configured: the default leaves it its own.
		Session:       wire.SessionOptions{ResumeMs: uint64(max(resumeS, 0)) * 1000, DeadS: uint64(cfg.Runtime.DeadS), IdleS: uint64(cfg.Runtime.IdleS)},
		ResumeTimeout: time.Duration(resumeS) * time.Second,
		Compress:      session.CompressLimits{MaxWindow: cfg.Network.CompressMaxWindow, MaxRatio: cfg.Network.CompressMaxRatio},
		MaxPipes:      cfg.Network.ClientMaxPipes,
		MaxLanes:      cfg.Runtime.SessionMaxLanes,
	})
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
