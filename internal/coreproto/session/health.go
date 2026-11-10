package session

import (
	"context"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/djylb/nps/lib/healthcheck"
)

// healthWriteTimeout bounds opening the stream and writing one report.
const healthWriteTimeout = 10 * time.Second

func healthToWire(g healthcheck.Group) wire.HealthGroup {
	return wire.HealthGroup{Name: g.Name, Type: g.Type, Path: g.Path, Interval: uint64(g.Interval),
		Timeout: uint64(g.Timeout), Fall: uint64(g.Fall), Targets: g.Targets}
}

func healthFromWire(g wire.HealthGroup) healthcheck.Group {
	return healthcheck.Group{Name: g.Name, Type: g.Type, Path: g.Path, Interval: int(g.Interval),
		Timeout: int(g.Timeout), Fall: int(g.Fall), Targets: g.Targets}
}

// CanHealth implements coreapi.Session.
func (s *serverSession) CanHealth() bool { return !s.visitorOnly && s.t.hello.HasCap(wire.CapHealth) }

// SetHealth implements coreapi.Session.
func (s *serverSession) SetHealth(groups []healthcheck.Group) {
	list := make([]wire.HealthGroup, len(groups))
	for i, g := range groups {
		list[i] = healthToWire(g)
	}
	slices.SortFunc(list, func(a, b wire.HealthGroup) int { return strings.Compare(a.Name, b.Name) })
	s.healthMu.Lock()
	s.health = list
	s.healthMu.Unlock()
	s.kick()
}

func (s *serverSession) healthGroups() []wire.HealthGroup {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	return s.health
}

// serveHealth reads the records of a HEALTH stream until it ends (§5.3).
func (s *serverSession) serveHealth(lc *linkConn) {
	defer func() { _ = lc.Close() }()
	h := s.a.hooks.Load()
	for {
		n, err := wire.ReadVarintFrom(lc)
		if err != nil {
			return
		}
		if n > wire.MaxHealthRecord {
			lc.abort(wire.CodeMalformed)
			return
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(lc, b); err != nil {
			return
		}
		r, err := wire.ParseHealthReport(b)
		if err != nil {
			lc.abort(wire.ErrorCode(err))
			return
		}
		if h != nil && h.Health != nil {
			h.Health(s, healthcheck.Report{Group: r.Name, Addr: r.Addr, Up: r.Up})
		}
	}
}

// healthSink writes reports on one stream, opened with the first of them.
// Its one caller is the health reporter.
type healthSink struct {
	s  *clientSession
	lc *linkConn
}

// ReportHealth implements the sink of coreapi.HealthRunner.
func (h *healthSink) ReportHealth(r healthcheck.Report) error {
	if h.lc == nil {
		ctx, cancel := context.WithTimeout(context.Background(), healthWriteTimeout)
		st, err := h.s.zc.OpenStreamSync(ctx)
		cancel()
		if err != nil {
			return err
		}
		lc := &linkConn{Stream: st}
		if err := st.SetPreamble(wire.AppendOpenInfo(nil, wire.OpHealth)); err != nil {
			lc.abort(wire.CodeInternal)
			return err
		}
		h.lc = lc
	}
	_ = h.lc.SetWriteDeadline(time.Now().Add(healthWriteTimeout))
	if _, err := h.lc.Write(wire.AppendHealthReport(nil, wire.HealthReport{Name: r.Group, Addr: r.Addr, Up: r.Up})); err != nil {
		// Part of a record may be out: the next report needs a new stream.
		h.lc.abort(wire.CodeInternal)
		h.lc = nil
		return err
	}
	return nil
}

// applyHealth gives the runner what the snapshot lists, starting it on the
// first non-empty list.
func (s *clientSession) applyHealth(list []wire.HealthGroup) {
	if s.d.cfg.Health == nil || s.healthRunner == nil && len(list) == 0 {
		return
	}
	if s.healthRunner == nil {
		s.healthRunner = s.d.cfg.Health(&healthSink{s: s})
	}
	groups := make([]healthcheck.Group, len(list))
	for i, g := range list {
		groups[i] = healthFromWire(g)
	}
	s.healthRunner.Set(groups)
}
