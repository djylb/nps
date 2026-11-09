package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/zmuxio/zmux-go/shaping/bundle"
)

const shapingRefresh = time.Hour

// shapingSource fetches and caches what session.Options.ShapingSource gives
// the entry; nothing here is known to the protocol.
type shapingSource struct {
	url, token, file string
	pub              ed25519.PublicKey
	client           *http.Client
}

// newShapingSource returns the source of cfg, nil when none is configured.
func newShapingSource(cfg *servercfg.Snapshot) (*shapingSource, error) {
	n := cfg.Network
	n.ShapingFile = runenv.GetPath(n.ShapingFile)
	if n.ShapingURL == "" {
		if _, err := os.Stat(n.ShapingFile); err != nil {
			return nil, nil
		}
	}
	s := &shapingSource{url: n.ShapingURL, token: n.ShapingToken, file: n.ShapingFile, client: &http.Client{Timeout: 30 * time.Second}}
	if n.ShapingPubKey != "" {
		k, err := hex.DecodeString(n.ShapingPubKey)
		if err != nil || len(k) != ed25519.PublicKeySize {
			return nil, errors.New("shaping_pubkey: not a hex public key")
		}
		s.pub = k
	}
	return s, nil
}

func (s *shapingSource) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	r, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", r.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, bundle.DefaultMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if s.pub != nil {
		sig, err := hex.DecodeString(r.Header.Get("X-Bundle-Signature"))
		if err != nil || !ed25519.Verify(s.pub, body, sig) {
			return nil, errors.New("bad signature")
		}
	}
	return body, nil
}

func (s *shapingSource) load(ctx context.Context) ([]byte, error) {
	if s.url != "" {
		return s.fetch(ctx)
	}
	return os.ReadFile(s.file)
}

// loadParsed loads the bundle and checks it: one over the size limit or
// invalid is an error. next is when the bundle itself asks to be looked at
// again (zero for no preference).
func (s *shapingSource) loadParsed(ctx context.Context) (b []byte, next time.Time, err error) {
	if b, err = s.load(ctx); err != nil {
		return nil, time.Time{}, err
	}
	if len(b) > bundle.DefaultMaxBytes {
		return nil, time.Time{}, errors.New("over the size limit")
	}
	parsed, err := bundle.Parse(b, bundle.ParseOptions{})
	if err != nil {
		return nil, time.Time{}, err
	}
	return b, parsed.NextChange(), nil
}

// run keeps the bundle current until ctx ends. A failure keeps what is in
// use and is retried with a growing delay until it succeeds; one over the
// size limit or invalid is not given to anybody.
func (s *shapingSource) run(ctx context.Context, set func([]byte)) {
	l := shapingLoop{load: s.loadParsed, set: set, refresh: shapingRefresh, retryMin: shapingRetryMin, retryMax: shapingRetryMax}
	l.run(ctx)
}

// The delay before retrying a failed load: it starts at shapingRetryMin,
// doubles on each failure and stops growing at shapingRetryMax. The address
// of the bundle may only be reachable once this server itself is up, so a
// failure at start is expected and must not wait out a refresh.
const (
	shapingRetryMin = 5 * time.Second
	shapingRetryMax = 5 * time.Minute
)

// retryDelay is the delay after the n-th (from 0) failure in a row: lo
// doubled n times up to hi, less up to a tenth so that servers that failed
// together do not retry together; hi is never exceeded.
func retryDelay(n int, lo, hi time.Duration) time.Duration {
	d := lo
	for ; n > 0 && d < hi; n-- {
		d *= 2
	}
	d = min(d, hi)
	if j := int64(d / 10); j > 0 {
		d -= time.Duration(rand.Int64N(j + 1))
	}
	return d
}

// shapingLoop is the refresh schedule of a bundle source.
type shapingLoop struct {
	load     func(context.Context) (b []byte, next time.Time, err error)
	set      func([]byte)
	refresh  time.Duration
	retryMin time.Duration
	retryMax time.Duration
}

func (l *shapingLoop) run(ctx context.Context) {
	var last []byte
	fails := 0
	for {
		wait := l.refresh
		b, next, err := l.load(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			wait = retryDelay(fails, l.retryMin, l.retryMax)
			if fails == 0 {
				log.Warn("shaping source not loaded, retrying", "err", err)
			} else {
				log.Debug("shaping source still not loaded", "err", err, "retry", wait)
			}
			fails++
		} else {
			if fails > 0 {
				log.Info("shaping source loaded", "failures", fails)
				fails = 0
			}
			if !bytes.Equal(b, last) {
				last = b
				l.set(b)
			}
			if d := time.Until(next); d > time.Second && d < wait {
				wait = d
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
