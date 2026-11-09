package session

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/zmuxio/zmux-go"
	"github.com/zmuxio/zmux-go/zmuxws"
)

// httpPairField is the request field that pairs the two halves of a split
// carrier, on both ends (zmux HTTPOptions.PairField): zmux's default names
// the software, and a request field is visible to every intermediary (§2.1b).
const httpPairField = "X-Client-Trace"

// httpAttemptTimeout bounds one kind of an http candidate that has another
// to fall back on: a path that blackholes a kind must not use up the whole
// dial timeout.
const httpAttemptTimeout = 8 * time.Second

// HTTPCarriers implements coreapi.Entry.
func (a *Acceptor) HTTPCarriers(next http.Handler, f coreapi.Forwarding) (http.Handler, error) {
	return zmux.NewHTTPHandler(a.zl, &zmux.HTTPOptions{Next: next, Trusted: trustedOf(f), PairField: httpPairField})
}

// attempts returns what Dial and the redial go through for cands: every
// (version, kind) of an http candidate, the candidate last good first.
func (d *Dialer) attempts(cands []connprofile.Candidate) []connprofile.Candidate {
	return connprofile.Order(connprofile.Attempts(cands), d.last)
}

// attemptTimeout is the limit of one try of c.
func (d *Dialer) attemptTimeout(c connprofile.Candidate) time.Duration {
	if c.HTTPCarrier() || c.Attempt != "" {
		return min(d.cfg.Timeout, httpAttemptTimeout)
	}
	return d.cfg.Timeout
}

// httpTransport dials every request of c at the candidate's address, with
// the TLS of a wss carrier: the outer TLS is not what the server's identity
// rests on (§3.2). versions are the HTTP versions its TLS offers.
func (d *Dialer) httpTransport(c connprofile.Candidate, versions []string) *http.Transport {
	cd := &captureDialer{dial: d.cfg.Dial}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return cd.DialContext(ctx, "tcp", c.Addr)
	}
	// Requests of a carrier live as long as it does; none is reused later.
	t := &http.Transport{DialContext: dial, DisableKeepAlives: true, ForceAttemptHTTP2: slices.Contains(versions, connprofile.HTTP2),
		DisableCompression: true, ResponseHeaderTimeout: 0}
	if c.Carrier == "https" || c.Carrier == "wss" {
		t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			nc, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			tc := tls.Client(nc, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: tlsProtos(versions), ServerName: serverName(c)})
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = nc.Close()
				return nil, err
			}
			return tc, nil
		}
	}
	return t
}

// tlsProtos are the ALPN names of the TCP versions of an http list (§2.4b).
func tlsProtos(versions []string) []string {
	var out []string
	for _, v := range versions {
		if v == connprofile.HTTP11 || v == connprofile.HTTP2 {
			out = append(out, v)
		}
	}
	return out
}

// httpClient follows no redirect, as the WebSocket carrier does.
func httpClient(t *http.Transport) *http.Client {
	return &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// httpURL is the URL of the requests of c; the Host field is the URL's, and
// c.Addr is what is dialed (httpTransport).
func httpURL(c connprofile.Candidate) string {
	host := c.Host
	if host == "" {
		host = c.Addr
	}
	scheme := "http"
	if c.Carrier == "https" || c.Carrier == "wss" {
		scheme = "https"
	}
	return scheme + "://" + host + c.Path
}

// dialHTTP opens a streaming or split carrier for one attempt of an http
// candidate: never Bound, as the request's connection is not end to end
// (§3.2), and so of any network.
func (d *Dialer) dialHTTP(ctx context.Context, c connprofile.Candidate) (carrier, error) {
	client := httpClient(d.httpTransport(c, c.HTTP))
	// The caller ends ctx once the carrier is dialed, which for a request
	// is before it is under way; closing the carrier ends it instead.
	ctx = context.WithoutCancel(ctx)
	var p zmux.PipeIO
	var err error
	switch c.Kinds[0] {
	case connprofile.KindStreaming:
		p, err = zmux.HTTPPipe(ctx, client, httpURL(c), nil, zmux.DirBidirectional, nil)
	case connprofile.KindSplit:
		p, err = (&zmux.HTTPSplitOptions{PairField: httpPairField}).Pipe(ctx, client, httpURL(c), nil, nil)
	default:
		err = errors.New("unknown http carrier kind " + c.Kinds[0])
	}
	if err != nil {
		return carrier{}, err
	}
	return carrier{pipe: p}, nil
}

// httpMode is the zmux mode of the pipes of an http candidate in a
// multi-pipe session (§14): zmux chooses the kind, stable first, and caches
// the failures. Its pipes never ask for Bound (§3.2).
func (d *Dialer) httpMode(m connprofile.Candidate) (zmux.Mode, error) {
	var cands []zmux.Candidate
	seen := map[string]bool{}
	for _, a := range m.Attempts() {
		k := a.Kinds[0]
		if seen[k] {
			continue
		}
		seen[k] = true
		switch k {
		case connprofile.KindWebSocket:
			cd := &captureDialer{dial: d.cfg.Dial}
			scheme := map[string]string{"http": "ws", "https": "wss"}[m.Carrier]
			tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, ServerName: serverName(m)}
			protocols := []string{zmuxws.NoSubprotocol}
			if m.Subprotocol != "" {
				protocols = []string{m.Subprotocol}
			}
			o := &zmuxws.DialOptions{Protocols: protocols, TLSConfig: tlsCfg, NoRedirects: true, Dialer: cd}
			if m.Host != "" {
				o.Header = http.Header{"Host": {m.Host}}
			}
			cands = append(cands, zmuxws.Candidate(scheme+"://"+m.Addr+m.Path, o))
		case connprofile.KindStreaming:
			cands = append(cands, zmux.StreamingCandidate(httpClient(d.httpTransport(m, m.HTTP)), httpURL(m), nil))
		case connprofile.KindSplit:
			c, err := (&zmux.HTTPSplitOptions{PairField: httpPairField}).Candidate(httpClient(d.httpTransport(m, m.HTTP)), httpURL(m), nil)
			if err != nil {
				return zmux.Mode{}, err
			}
			cands = append(cands, c)
		}
	}
	return zmux.AutoMode(cands...), nil
}

// maintainHTTP keeps the pipes of the http member m by its mode. A pipe the
// session started on is outside the pool, which cannot adopt it: once a pipe
// of the pool stands it is retired, make before break.
func (s *clientSession) maintainHTTP(ctx context.Context, m connprofile.Candidate, o zmux.PipeOptions, boot bool) {
	mode, err := s.d.httpMode(m)
	if err != nil {
		log.Debug("pipe pool not started", "carrier", m.Carrier, "err", err)
		return
	}
	s.d.mu.Lock()
	pl := choose(s.d.record, false, false)
	s.d.mu.Unlock()
	o.Mode = &pl.mode
	var first *zmux.Pipe
	if pipes := s.zc.Pipes(); boot && len(pipes) > 0 {
		first = pipes[0]
	}
	pool := zmux.PipePool{Count: m.Count, Options: o, Mode: mode,
		ModePolicy: &zmux.ModePolicy{Cache: &s.d.kinds, Key: m.Key(), TimerMin: 3 * time.Second, TimerMax: 5 * time.Second}}
	s.wg.Go(func() {
		if err := s.zc.MaintainPipes(ctx, pool); err != nil && ctx.Err() == nil {
			log.Debug("pipe pool ended", "carrier", m.Carrier, "err", err)
		}
	})
	if first != nil {
		s.wg.Go(func() { s.retire(ctx, first, m) })
	}
}

// retire closes first once another pipe of m is usable.
func (s *clientSession) retire(ctx context.Context, first *zmux.Pipe, m connprofile.Candidate) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, p := range s.zc.Pipes() {
			st, ok := p.Stats()
			if !ok || p == first || st.Label != m.Key() || !st.PeerAttached || st.Outbound != zmux.OutboundActive || !st.ProbationUntil.IsZero() {
				continue
			}
			_ = first.Close()
			return
		}
		if _, ok := first.Stats(); !ok {
			return
		}
	}
}
