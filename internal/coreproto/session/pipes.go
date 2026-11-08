package session

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/connprofile"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/zmuxquic"
)

// firstConfigWait is how long the pools wait for the first CONFIG, whose
// record decides the mode of every further pipe (§3.2).
const firstConfigWait = 3 * time.Second

func (d *Dialer) multi() bool { return d.cfg.Pipes == connprofile.PipesMulti }

// sessionConfig is the zmux configuration of a session. Multi-pipe sessions
// have MaintainPipes as their redial source, which resume (§3.8) needs.
func (d *Dialer) sessionConfig() *zmux.Config {
	cfg := &zmux.Config{EnableDatagrams: true, MaxIncomingStreams: maxIncomingLinks, Scheduler: schedulerNamed(d.cfg.Scheduler)}
	if d.multi() {
		cfg.MaxPipes = max(pipesOf(connprofile.MultiMembers(d.cfg.Candidates)), 2)
	}
	return cfg
}

// pipesOf is how many pipes the members ask for together.
func pipesOf(members []connprofile.Candidate) int {
	n := 0
	for _, m := range members {
		n += m.Count * len(pipeDirs(m.Dirs))
	}
	return n
}

// pipeDirs is the pipes of one member, by direction from npc: a bidirectional
// one, or an uplink and a downlink as asked.
func pipeDirs(d connprofile.Dirs) []zmux.Direction {
	if d&connprofile.DirBidi != 0 {
		return []zmux.Direction{zmux.DirBidirectional}
	}
	var out []zmux.Direction
	if d&connprofile.DirUp != 0 {
		out = append(out, zmux.DirSendOnly)
	}
	if d&connprofile.DirDown != 0 {
		out = append(out, zmux.DirReceiveOnly)
	}
	return out
}

// schedulerNamed is the scheduler of a name of connprofile.Schedulers; any
// other name is affinity.
func schedulerNamed(name string) zmux.Scheduler {
	switch name {
	case connprofile.SchedulerSessionAffinity:
		return zmux.SessionAffinity()
	case connprofile.SchedulerSpreadETA:
		return zmux.SpreadETA()
	case connprofile.SchedulerSpreadRoundRobin:
		return zmux.SpreadRoundRobin()
	case connprofile.SchedulerSpreadRateWeighted:
		return zmux.SpreadRateWeighted()
	}
	return zmux.Affinity()
}

// pipeOptions are the options of the pipes of c: the tier as a priority
// counted from the best tier in use, the weight, and standby in both
// directions.
func pipeOptions(c connprofile.Candidate, best int) zmux.PipeOptions {
	tier := uint64(max(c.Tier-best, 0))
	o := zmux.PipeOptions{Label: c.Key(), SendPriority: &tier, ReceivePriority: tier}
	if c.Weight > 0 {
		w := uint64(c.Weight)
		o.Weight = &w
	}
	if c.Standby {
		standby := true
		o.SendStandby, o.ReceiveStandby = &standby, true
	}
	return o
}

func bestTier(members []connprofile.Candidate) int {
	best, have := 0, false
	for _, m := range members {
		if !m.Standby && (!have || m.Tier < best) {
			best, have = m.Tier, true
		}
	}
	return best
}

// maintain keeps the pipes of every member up until the session ends: one
// pool per candidate. The first pipe belongs to the pool of the candidate it
// was dialed on, so that its loss is replaced like any other.
func (s *clientSession) maintain(ctx context.Context, boot connprofile.Candidate) {
	t := time.NewTimer(firstConfigWait)
	defer t.Stop()
	select {
	case <-s.configured:
	case <-t.C:
	case <-ctx.Done():
		return
	}
	members := connprofile.MultiMembers(s.d.cfg.Candidates)
	best := bestTier(members)
	for _, m := range members {
		var first atomic.Bool
		first.Store(m.Key() == boot.Key())
		pool := zmux.PipePool{Count: m.Count, Options: pipeOptions(m, best)}
		pool.Join = func(ctx context.Context, _ *zmux.Conn, o zmux.PipeOptions) ([]*zmux.Pipe, error) {
			if first.CompareAndSwap(true, false) {
				return s.adoptFirst(o)
			}
			return s.join(ctx, m, o)
		}
		s.wg.Go(func() {
			if err := s.zc.MaintainPipes(ctx, pool); err != nil && ctx.Err() == nil {
				log.Debug("pipe pool ended", "carrier", m.Carrier, "err", err)
			}
		})
	}
}

// adoptFirst gives the pipe the session started on the settings its pool
// would have given it.
func (s *clientSession) adoptFirst(o zmux.PipeOptions) ([]*zmux.Pipe, error) {
	pipes := s.zc.Pipes()
	if len(pipes) == 0 {
		return nil, errors.New("no first pipe")
	}
	p := pipes[0]
	if o.SendPriority != nil {
		_ = p.SetSendPriority(*o.SendPriority)
	}
	_ = p.SetReceivePriority(o.ReceivePriority)
	if o.Weight != nil {
		_ = p.SetWeight(*o.Weight)
	}
	if o.SendStandby != nil {
		_ = p.SetSendStandby(*o.SendStandby)
		_ = p.SetReceiveStandby(o.ReceiveStandby)
	}
	return []*zmux.Pipe{p}, nil
}

// join dials and joins one member of m. A member of two directions lives and
// dies whole: the pool replaces it together.
func (s *clientSession) join(ctx context.Context, m connprofile.Candidate, o zmux.PipeOptions) ([]*zmux.Pipe, error) {
	var out []*zmux.Pipe
	for _, dir := range pipeDirs(m.Dirs) {
		o.Direction = dir
		p, err := s.joinOne(ctx, m, o)
		if err != nil {
			for _, q := range out {
				_ = q.Close()
			}
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// joinOne is dialCandidate for a further pipe: the mode of §3.2 per candidate
// and network, and Sealed on a new carrier after a failing Bound.
func (s *clientSession) joinOne(ctx context.Context, m connprofile.Candidate, o zmux.PipeOptions) (*zmux.Pipe, error) {
	d := s.d
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()
	ca, err := d.dialCarrier(ctx, m)
	if err != nil {
		return nil, err
	}
	key := m.Key() + "|" + ca.network
	d.mu.Lock()
	pl := choose(d.record, ca.secure, d.boundFailedLocked(key))
	d.mu.Unlock()
	o.Mode = &pl.mode
	if pl.mode != zmux.Bound {
		return s.zc.AddPipe(ctx, ca.pipe, o)
	}
	bctx, bcancel := context.WithTimeout(ctx, boundLimit)
	p, err := s.zc.AddPipe(bctx, ca.pipe, o)
	bcancel()
	if err == nil || !pl.fallback || ctx.Err() != nil {
		return p, err
	}
	log.Debug("bound failed on a join, trying sealed", "carrier", m.Carrier, "err", err)
	if ca, err = d.dialCarrier(ctx, m); err != nil {
		return nil, err
	}
	sealed := zmux.Sealed
	o.Mode = &sealed
	if p, err = s.zc.AddPipe(ctx, ca.pipe, o); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.noteBoundFailedLocked(key)
	d.mu.Unlock()
	return p, nil
}

// dialQUIC opens a QUIC connection of its own for one carrier; the connection
// ends with its pipe.
func (d *Dialer) dialQUIC(ctx context.Context, c connprofile.Candidate, t *tls.Config) (carrier, error) {
	q := zmuxquic.QUICConfig(&quic.Config{KeepAlivePeriod: 10 * time.Second, MaxIdleTimeout: 30 * time.Second})
	dial := d.cfg.DialQUIC
	if dial == nil {
		dial = func(ctx context.Context, addr string, t *tls.Config, q *quic.Config) (*quic.Conn, error) {
			return quic.DialAddr(ctx, addr, t, q)
		}
	}
	qc, err := dial(ctx, c.Addr, t, q)
	if err != nil {
		return carrier{}, err
	}
	p, err := zmuxquic.OpenPipe(ctx, qc)
	if err != nil {
		_ = qc.CloseWithError(0, "")
		return carrier{}, err
	}
	p.R = &quicPipeReader{r: p.R, qc: qc}
	return carrier{pipe: p, secure: true, network: networkOf(qc.LocalAddr())}, nil
}

// quicPipeReader closes the QUIC connection of a pipe with the pipe, which
// the stream's own close does not. The other methods are the capabilities of
// the reading half.
type quicPipeReader struct {
	r  io.ReadCloser
	qc *quic.Conn
}

func (r *quicPipeReader) Read(p []byte) (int, error) { return r.r.Read(p) }

func (r *quicPipeReader) Close() error {
	err := r.r.Close()
	_ = r.qc.CloseWithError(0, "")
	return err
}

func (r *quicPipeReader) CarrierConn() any {
	if c, ok := r.r.(interface{ CarrierConn() any }); ok {
		return c.CarrierConn()
	}
	return nil
}

func (r *quicPipeReader) SetReadDeadline(t time.Time) error {
	if d, ok := r.r.(interface{ SetReadDeadline(time.Time) error }); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}
