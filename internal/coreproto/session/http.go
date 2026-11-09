package session

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"slices"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/internal/coreproto/coreapi"
	"github.com/djylb/nps/lib/connprofile"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
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
	h, err := zmux.NewHTTPHandler(a.zl, &zmux.HTTPOptions{Next: next, Trusted: trustedOf(f), PairField: httpPairField})
	if err != nil || len(a.admitKey) == 0 {
		return h, err
	}
	ex, err := a.exchangeHandler(f)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ex.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), exchangeNextKey{}, h)))
	}), nil
}

// exchangeNextKey holds what a request that is no exchange goes on to.
type exchangeNextKey struct{}

// exchangeHandler is the one handler of the exchange carriers of the entry:
// it holds their state, and every path of the entry has the same key. What is
// not an exchange goes on to the handler of the path it came in by.
func (a *Acceptor) exchangeHandler(f coreapi.Forwarding) (*zmux.HTTPExchangeHandler, error) {
	a.exchange.once.Do(func() {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Context().Value(exchangeNextKey{}).(http.Handler).ServeHTTP(w, r)
		})
		a.exchange.h, a.exchange.err = zmux.NewHTTPExchangeHandler(a.zl, &zmux.HTTPExchangeConfig{PSK: a.admitKey, PairField: httpPairField, Next: next, Trusted: trustedOf(f)})
	})
	return a.exchange.h, a.exchange.err
}

// attempts returns what Dial and the redial go through for cands: every
// (version, kind) of an http candidate, the candidate last good first.
func (d *Dialer) attempts(cands []connprofile.Candidate) []connprofile.Candidate {
	all := connprofile.Attempts(cands)
	usable := all[:0:0]
	for _, a := range all {
		// §2.4b: the first connection is over TCP unless the list has only
		// h3; h3 follows once it has worked.
		if a.Carrier != "https" || a.HTTP[0] != connprofile.HTTP3 || d.h3Known[a.Raw] || onlyHTTP3(cands, a) {
			usable = append(usable, a)
		}
	}
	return connprofile.Order(usable, d.last)
}

// onlyHTTP3 reports whether the candidate that attempt a came from names no
// version but h3.
func onlyHTTP3(cands []connprofile.Candidate, a connprofile.Candidate) bool {
	return slices.ContainsFunc(cands, func(c connprofile.Candidate) bool { return c.Raw == a.Raw && c.OnlyHTTP3() })
}

// noteH3 records how an h3 attempt went: a failure is told once per change.
func (d *Dialer) noteH3(c connprofile.Candidate, ok bool, err error) {
	if c.Carrier != "https" || c.HTTP[0] != connprofile.HTTP3 {
		return
	}
	d.mu.Lock()
	known := d.h3Known[c.Raw]
	if ok {
		d.h3Known[c.Raw] = true
	} else {
		delete(d.h3Known, c.Raw)
	}
	d.mu.Unlock()
	if !ok && (known || c.OnlyHTTP3()) {
		log.Warn("h3 is unavailable, going on without it", "server", c.Addr, "err", err)
	}
}

// h3Client is the HTTP/3 client of candidate c, which its attempts share:
// their carriers are request streams of one QUIC connection. It stays until
// the session ends (closeHTTP).
func (d *Dialer) h3Client(c connprofile.Candidate) *http.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cl := d.h3Clients[c.Raw]; cl != nil {
		return cl
	}
	rt := &h3Transport{host: c.Host}
	rt.t = &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13, ServerName: serverName(c), NextProtos: []string{http3.NextProtoH3}},
		QUICConfig:      &quic.Config{KeepAlivePeriod: 10 * time.Second, MaxIdleTimeout: 30 * time.Second},
		Dial: func(ctx context.Context, addr string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
			conn, err := d.dialQUICConn(ctx, addr, tc, qc)
			if err == nil {
				rt.conn.Store(conn)
			}
			return conn, err
		},
	}
	d.closers = append(d.closers, func() { _ = rt.t.Close() })
	cl := &http.Client{Transport: rt, CheckRedirect: noRedirect}
	d.h3Clients[c.Raw] = cl
	return cl
}

// h3Transport runs the requests of a candidate over its QUIC connection. It
// sends the host parameter as the request's Host, the URL naming the address
// to dial, and names that connection to a carrier's body before the headers
// go out, which is what makes the carrier declare the Bound contract. (zmuxh3
// has this client, but needs a webtransport-go that does not build against
// the quic-go of this module.)
type h3Transport struct {
	t    *http3.Transport
	host string
	conn atomic.Pointer[quic.Conn]
}

func (t *h3Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.host != "" {
		r = r.Clone(r.Context())
		r.Host = t.host
	}
	if b, ok := r.Body.(zmux.HTTPCarrierBody); ok {
		r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) {
			if qc := t.conn.Load(); qc != nil {
				b.SetSecureConnection(quicState{qc})
			}
		}}))
	}
	return t.t.RoundTrip(r)
}

// quicState is the TLS state of a QUIC connection, read when asked.
type quicState struct{ qc *quic.Conn }

func (s quicState) ConnectionState() tls.ConnectionState { return s.qc.ConnectionState().TLS }

// exchangeClient is the client of the bounded requests of an exchange
// carrier, which unlike the others reuse their connections.
func (d *Dialer) exchangeClient(c connprofile.Candidate) *http.Client {
	t := d.httpTransport(c, c.HTTP)
	t.DisableKeepAlives, t.MaxIdleConnsPerHost, t.IdleConnTimeout = false, 8, 30*time.Second
	d.mu.Lock()
	d.closers = append(d.closers, t.CloseIdleConnections)
	d.mu.Unlock()
	return httpClient(t)
}

func (d *Dialer) exchangeConfig(c connprofile.Candidate) (*zmux.HTTPExchangeConfig, error) {
	key, err := hex.DecodeString(c.AdmitKey)
	if err != nil {
		return nil, err
	}
	return &zmux.HTTPExchangeConfig{PSK: key, PairField: httpPairField}, nil
}

// closeHTTP ends what the HTTP carriers of the last session kept open.
func (d *Dialer) closeHTTP() {
	d.mu.Lock()
	closers := d.closers
	d.closers, d.h3Clients = nil, map[string]*http.Client{}
	d.mu.Unlock()
	for _, f := range closers {
		f()
	}
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
	return &http.Client{Transport: t, CheckRedirect: noRedirect}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// h3URL is httpURL for the HTTP/3 transport, which dials the URL's host.
func h3URL(c connprofile.Candidate) string { return "https://" + c.Addr + c.Path }

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

// dialHTTP opens a carrier for one attempt of an http candidate. Only the
// h3 kind can be Bound: a request stream of QUIC is end to end, the others'
// connection is not (§3.2).
func (d *Dialer) dialHTTP(ctx context.Context, c connprofile.Candidate) (carrier, error) {
	// The caller ends ctx once the carrier is dialed, which for a request
	// is before it is under way; closing the carrier ends it instead.
	ctx = context.WithoutCancel(ctx)
	var p zmux.PipeIO
	var err error
	switch c.Kinds[0] {
	case connprofile.KindHTTP3:
		if p, err = zmux.HTTPPipe(ctx, d.h3Client(c), h3URL(c), nil, zmux.DirBidirectional, nil); err != nil {
			return carrier{}, err
		}
		return carrier{pipe: p, secure: true}, nil
	case connprofile.KindStreaming:
		p, err = zmux.HTTPPipe(ctx, httpClient(d.httpTransport(c, c.HTTP)), httpURL(c), nil, zmux.DirBidirectional, nil)
	case connprofile.KindSplit:
		p, err = (&zmux.HTTPSplitOptions{PairField: httpPairField}).Pipe(ctx, httpClient(d.httpTransport(c, c.HTTP)), httpURL(c), nil, nil)
	case connprofile.KindExchange:
		var cfg *zmux.HTTPExchangeConfig
		if cfg, err = d.exchangeConfig(c); err == nil {
			p, err = zmux.HTTPExchangePipe(ctx, d.exchangeClient(c), httpURL(c), cfg)
		}
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
			protocols := []string{legacySubprotocol}
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
		case connprofile.KindHTTP3:
			h3 := zmux.StreamingCandidate(d.h3Client(m), h3URL(m), nil)
			h3.Kind = zmux.CarrierHTTP3
			cands = append(cands, h3)
		case connprofile.KindExchange:
			cfg, err := d.exchangeConfig(m)
			if err != nil {
				return zmux.Mode{}, err
			}
			cands = append(cands, zmux.ExchangeCandidate(d.exchangeClient(m), httpURL(m), cfg))
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
