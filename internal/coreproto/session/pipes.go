package session

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/connprofile"
	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
	"github.com/djylb/timeshim"
	"github.com/quic-go/quic-go"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/zmuxquic"
)

// firstConfigWait is how long the pools wait for the first CONFIG, whose
// record decides the mode of every further pipe (§3.2).
const firstConfigWait = 3 * time.Second

func (d *Dialer) multi() bool { return d.cfg.Pipes == connprofile.PipesMulti }

// sessionConfig is the zmux configuration of session s. Resume (§3.8) needs a
// redial source: multi-pipe sessions have MaintainPipes, the others Redial.
func (d *Dialer) sessionConfig(s *clientSession) *zmux.Config {
	cfg := boundBuffers(&zmux.Config{EnableDatagrams: true, MaxIncomingStreams: maxIncomingLinks, MaxLanes: maxLanesOf(d.cfg.MaxLanes), Scheduler: schedulerNamed(d.cfg.Scheduler),
		ResumeTimeout: d.resumeTimeout()})
	cfg.HandshakeShaper = d.joinShape
	cfg.Shaper, cfg.ShapingBudget = s.plan.sessionShaper(s.boot)
	switch {
	case d.multi():
		// Rotating to a preferred kind and adopting a first pipe of an HTTP
		// candidate need room for one pipe more.
		cfg.MaxPipes = max(pipesOf(connprofile.MultiMembers(d.cfg.Candidates))+1, 2)
	case cfg.ResumeTimeout > 0:
		cfg.Redial, cfg.RecordPolicy = s.redial, s.redialRecord
		// redial paces itself (redialWait); zmux adds no wait of its own.
		cfg.RedialBackoff = noRedialBackoff
	}
	return cfg
}

const (
	// redialFirst is the full-jitter window of the first redial of a
	// suspension: every client of a server that lost its network redials at
	// once, spread over it rather than in one burst.
	redialFirst = time.Second
	// redialMax caps the window of the later redials, each of which doubles
	// it.
	redialMax = time.Minute
	// recoveryConcurrency is how many recovery carriers the npc process
	// dials at a time (recoveryGate).
	recoveryConcurrency = 128
)

// noRedialBackoff leaves the pacing of Redial to redialWait: zmux's own
// backoff starts with an immediate call and has a ±20 % jitter only.
var noRedialBackoff = zmux.Backoff{Min: time.Nanosecond, Max: time.Nanosecond, Jitter: -1}

// recoveryGate bounds the carriers that recovering sessions dial at once,
// TLS handshake included, over every profile of the process: a server that
// comes back sees the clients it lost in turns, not all of their handshakes
// together.
type recoveryGate struct{ slots chan struct{} }

func newRecoveryGate(n int) *recoveryGate { return &recoveryGate{slots: make(chan struct{}, n)} }

// processRecovery is the gate of the npc process; a Dialer takes it unless a
// test gives it another.
var processRecovery = newRecoveryGate(recoveryConcurrency) //nolint:gochecknoglobals // process-wide budget by design

// acquire waits for a slot until ctx ends; release gives it back.
func (g *recoveryGate) acquire(ctx context.Context) (release func(), err error) {
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// recoveryDial is dialCarrier for a recovering session, under the gate.
func (d *Dialer) recoveryDial(ctx context.Context, c connprofile.Candidate) (carrier, error) {
	release, err := d.recovery.acquire(ctx)
	if err != nil {
		return carrier{}, err
	}
	defer release()
	return d.dialCarrier(ctx, c)
}

// redialWait is the wait before the next redial of s and counts it: the
// first of a suspension is in [0, redialFirst), each after a failure in a
// window twice the last, up to redialMax; redialDone starts over.
func (s *clientSession) redialWait() time.Duration {
	s.redialMu.Lock()
	n := s.redialTries
	s.redialTries++
	s.redialMu.Unlock()
	w := redialFirst
	for i := 0; i < n && w < redialMax; i++ {
		w *= 2
	}
	return time.Duration(s.d.jitter() * float64(min(w, redialMax)))
}

// redial gives zmux a new carrier for the only pipe of a session, the
// candidates in the order of Dial. zmux joins it right away with the mode of
// redialRecord, and calls again when that fails: a carrier that connected but
// did not join makes way for the candidate after it.
func (s *clientSession) redial(ctx context.Context, _ zmux.Direction) (zmux.PipeIO, error) {
	d := s.d
	if err := timeshim.Sleep(ctx, d.clock, s.redialWait()); err != nil {
		return zmux.PipeIO{}, err
	}
	d.mu.Lock()
	cands := d.attempts(d.cfg.Candidates)
	d.mu.Unlock()
	s.redialMu.Lock()
	tried := s.redialKey
	s.redialMu.Unlock()
	if i := slices.IndexFunc(cands, func(c connprofile.Candidate) bool { return c.Key() == tried }); i >= 0 {
		cands = append(cands[i+1:], cands[:i+1]...)
	}
	errs := []error{errors.New("no candidate can carry a pipe")}
	for _, c := range cands {
		dctx, cancel := context.WithTimeout(ctx, d.attemptTimeout(c))
		ca, err := d.recoveryDial(dctx, c)
		cancel()
		if err == nil {
			s.planRedial(c, ca)
			return ca.pipe, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return zmux.PipeIO{}, errors.Join(errs...)
}

// planRedial picks the mode of §3.2 for a carrier about to be joined. A join
// shows its result only by zmux calling redial again, so an attempt in Bound
// that may fall back is followed, on that candidate and network, by one in
// Sealed.
func (s *clientSession) planRedial(c connprofile.Candidate, ca carrier) {
	key := c.Key() + "|" + ca.network
	s.redialMu.Lock()
	failed := s.redialBound[key]
	s.redialMu.Unlock()
	d := s.d
	d.mu.Lock()
	p := choose(d.record, ca.secure, failed || d.boundFailedLocked(key))
	d.mu.Unlock()
	s.redialMu.Lock()
	defer s.redialMu.Unlock()
	s.redialKey, s.redialMode = c.Key(), p.mode
	if p.mode == zmux.RecordBound && p.fallback {
		if s.redialBound == nil {
			s.redialBound = map[string]bool{}
		}
		s.redialBound[key] = true
	} else {
		delete(s.redialBound, key)
	}
}

// redialRecord is zmux.Config.RecordPolicy of a session that redials.
func (s *clientSession) redialRecord(zmux.RecordPolicyInput) zmux.RecordMode {
	s.redialMu.Lock()
	defer s.redialMu.Unlock()
	return s.redialMode
}

// redialDone is the end of a suspension: the candidate of the carrier that
// resumed the session is the last good one.
func (s *clientSession) redialDone() {
	s.redialMu.Lock()
	key := s.redialKey
	s.redialKey, s.redialBound, s.redialTries = "", nil, 0
	s.redialMu.Unlock()
	if key != "" {
		s.d.mu.Lock()
		s.d.last = key
		s.d.mu.Unlock()
	}
}

// event reports the suspensions of the session (zmux.Conn.HandleEvents: one
// at a time); one that comes once the session's callbacks drain is dropped.
func (s *clientSession) event(ev zmux.Event) {
	task, err := s.scope.Enter()
	if err != nil {
		return
	}
	defer task.End()
	switch ev.Kind {
	case zmux.EventSessionSuspended:
		s.suspendSince = time.Now()
		slogx.Emit(s.ctx, log, &logevent.SessionSuspended, "server", s.d.Server(), "carrier", s.carrier)
	case zmux.EventSessionResumed:
		s.redialDone()
		slogx.Emit(s.ctx, log, &logevent.SessionResumed, "server", s.d.Server(), "carrier", s.carrier, "dur_ms", time.Since(s.suspendSince).Milliseconds())
	default: // the pipe events are not logged here
	}
}

// pipesOf is how many pipes the members ask for together.
func pipesOf(members []connprofile.Candidate) int {
	n := 0
	for _, m := range members {
		n += m.Count
	}
	return n
}

// schedulerNamed is the scheduler of a name of connprofile.Schedulers; any
// other name is affinity.
func schedulerNamed(name string) zmux.Scheduler {
	switch name {
	case connprofile.SchedulerSessionAffinity:
		return zmux.SchedulerSessionAffinity()
	case connprofile.SchedulerSpreadETA:
		return zmux.SchedulerSpreadETA()
	case connprofile.SchedulerSpreadRoundRobin:
		return zmux.SchedulerSpreadRoundRobin()
	case connprofile.SchedulerSpreadRateWeighted:
		return zmux.SchedulerSpreadRateWeighted()
	}
	return zmux.SchedulerAffinity()
}

// pipeOptions are the options of the pipes of c: the tier as a priority
// counted from the best tier in use, the weight, and standby: in both
// directions for a standby candidate, else in the direction c does not
// prefer, which the pipe still carries when no other pipe can.
func pipeOptions(c connprofile.Candidate, best int) zmux.PipeOptions {
	tier := uint64(max(c.Tier-best, 0))
	o := zmux.PipeOptions{Label: c.Key(), SendPriority: zmux.Set(tier), ReceivePriority: zmux.Set(tier)}
	if c.Weight > 0 {
		o.Weight = zmux.Set(uint64(c.Weight))
	}
	switch {
	case c.Standby:
		o.SendStandby, o.ReceiveStandby = zmux.Set(true), zmux.Set(true)
	case c.Dirs.Prefers() == connprofile.DirUp:
		o.ReceiveStandby = zmux.Set(true)
	case c.Dirs.Prefers() == connprofile.DirDown:
		o.SendStandby = zmux.Set(true)
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
	members := s.d.poolMembers()
	best := bestTier(members)
	for _, m := range members {
		if m.HTTPCarrier() {
			s.maintainHTTP(ctx, m, pipeOptions(m, best), m.Raw == boot.Raw)
			continue
		}
		var first atomic.Bool
		first.Store(m.Raw == boot.Raw)
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
	_ = p.Update(zmux.PipeUpdate{SendPriority: o.SendPriority, ReceivePriority: o.ReceivePriority, Weight: o.Weight, SendStandby: o.SendStandby, ReceiveStandby: o.ReceiveStandby})
	return []*zmux.Pipe{p}, nil
}

// join dials and joins one pipe of m.
func (s *clientSession) join(ctx context.Context, m connprofile.Candidate, o zmux.PipeOptions) ([]*zmux.Pipe, error) {
	p, err := s.joinOne(ctx, m, o)
	if err != nil {
		if ctx.Err() == nil {
			log.Debug("pipe join failed", "carrier", m.Carrier, "addr", m.Addr, "err", err)
		}
		return nil, err
	}
	return []*zmux.Pipe{p}, nil
}

// joinOne is dialCandidate for a further pipe: the mode of §3.2 per candidate
// and network, and Sealed on a new carrier after a failing Bound.
func (s *clientSession) joinOne(ctx context.Context, m connprofile.Candidate, o zmux.PipeOptions) (*zmux.Pipe, error) {
	d := s.d
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()
	ca, err := d.recoveryDial(ctx, m)
	if err != nil {
		return nil, err
	}
	key := m.Key() + "|" + ca.network
	d.mu.Lock()
	pl := choose(d.record, ca.secure, d.boundFailedLocked(key))
	d.mu.Unlock()
	o.RecordMode = zmux.Set(pl.mode)
	if pl.mode != zmux.RecordBound {
		return s.zc.AddPipe(ctx, ca.pipe, o)
	}
	bctx, bcancel := context.WithTimeout(ctx, boundLimit)
	p, err := s.zc.AddPipe(bctx, ca.pipe, o)
	bcancel()
	if err == nil || !pl.fallback || ctx.Err() != nil {
		return p, err
	}
	log.Debug("bound failed on a join, trying sealed", "carrier", m.Carrier, "err", err)
	if ca, err = d.recoveryDial(ctx, m); err != nil {
		return nil, err
	}
	o.RecordMode = zmux.Set(zmux.RecordSealed)
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
// dialQUICConn opens a QUIC connection, keeping -local_ip.
func (d *Dialer) dialQUICConn(ctx context.Context, addr string, t *tls.Config, q *quic.Config) (*quic.Conn, error) {
	if d.cfg.DialQUIC != nil {
		return d.cfg.DialQUIC(ctx, addr, t, q)
	}
	return quic.DialAddr(ctx, addr, t, q)
}

func (d *Dialer) dialQUIC(ctx context.Context, c connprofile.Candidate, t *tls.Config) (carrier, error) {
	q := zmuxquic.QUICConfig(&quic.Config{KeepAlivePeriod: 10 * time.Second, MaxIdleTimeout: 30 * time.Second})
	qc, err := d.dialQUICConn(ctx, c.Addr, t, q)
	if err != nil {
		return carrier{}, err
	}
	p, err := zmuxquic.ConnPipe(ctx, qc)
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

// poolMembers are the candidates that carry pipes of a session: of a type=auto
// address only the entry that won, whose carrier the path lets through.
func (d *Dialer) poolMembers() []connprofile.Candidate {
	members := connprofile.MultiMembers(d.cfg.Candidates)
	d.mu.Lock()
	winner := d.autoWinner
	d.mu.Unlock()
	if winner == "" {
		return members
	}
	return slices.DeleteFunc(members, func(c connprofile.Candidate) bool { return c.Auto && c.Raw != winner })
}
