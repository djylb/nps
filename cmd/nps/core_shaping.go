package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/shapebundle"
	"github.com/zmuxio/zmux-go/shaping/bundle"
)

// The schedule of a bundle source: a bundle without next_change is looked
// at again after shapingRefresh; a failure is retried from shapingRetryMin,
// doubling up to shapingRetryMax. The address of the bundle may only be
// reachable once this server itself is up, so a failure at start must not
// wait out a refresh.
const (
	shapingRefresh  = time.Hour
	shapingRetryMin = 5 * time.Second
	shapingRetryMax = 5 * time.Minute
	// shapingCacheName is the cached bundle of shaping_url in the conf
	// directory: a restart without the network keeps the last one.
	shapingCacheName = "shaping_cache.json"
)

// shapingSource gives the entry the bundle of shaping_url, fetched, verified
// and cached by shapebundle, or that of shaping_file; nothing here is known
// to the protocol.
type shapingSource struct {
	fetch *shapebundle.FetcherConfig // shaping_url
	file  string                     // shaping_file, without shaping_url

	fetcher atomic.Pointer[shapebundle.Fetcher] // of shaping_url, once run
	reread  chan chan error                     // asks runFile to read now
	mu      sync.Mutex
	status  shapebundle.Status // of shaping_file, under mu
}

// errShapingNotRunning is a reload before the source runs.
var errShapingNotRunning = errors.New("shaping source not running")

// Reload fetches or reads the bundle now, as the dashboard asks, instead of
// at the next refresh; the source then goes on from this result.
func (s *shapingSource) Reload(ctx context.Context) error {
	if s.fetch != nil {
		f := s.fetcher.Load()
		if f == nil {
			return errShapingNotRunning
		}
		_, err := f.Refresh(ctx)
		return err
	}
	done := make(chan error, 1)
	select {
	case s.reread <- done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Kind is "url" for shaping_url, "file" for shaping_file.
func (s *shapingSource) Kind() string {
	if s.fetch != nil {
		return "url"
	}
	return "file"
}

// Status is what the source last did and when it looks again: that of the
// fetcher for shaping_url, the same for the reads of shaping_file.
func (s *shapingSource) Status() shapebundle.Status {
	if f := s.fetcher.Load(); f != nil {
		return f.Status()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *shapingSource) setStatus(u func(*shapebundle.Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u(&s.status)
}

// newShapingSource returns the source of cfg, nil when none is configured.
func newShapingSource(cfg *servercfg.Snapshot) (*shapingSource, error) {
	n := cfg.Network
	if n.ShapingURL == "" {
		file := runenv.GetPath(n.ShapingFile)
		if !runenv.FileExists(file) {
			return nil, nil
		}
		return &shapingSource{file: file, reread: make(chan chan error)}, nil
	}
	c := &shapebundle.FetcherConfig{
		URL: n.ShapingURL, Body: []byte("{}"), Token: n.ShapingToken,
		CacheDir: runenv.GetPath("conf"), CacheName: shapingCacheName,
		Interval: shapingRefresh, RetryMin: shapingRetryMin, RetryMax: shapingRetryMax,
	}
	if n.ShapingPubKey != "" {
		k, err := shapebundle.ParsePublicKey(n.ShapingPubKey)
		if err != nil {
			return nil, fmt.Errorf("shaping_pubkey: %w", err)
		}
		c.PublicKey = k
	}
	return &shapingSource{fetch: c}, nil
}

// run keeps the bundle current until ctx ends, giving set each one that
// changes. A failure keeps what is in use and is retried with a growing delay.
func (s *shapingSource) run(ctx context.Context, set func([]byte)) {
	var last []byte
	give := func(b *bundle.Bundle) {
		if raw := b.Raw(); !bytes.Equal(raw, last) {
			last = raw
			set(raw)
		}
	}
	if s.fetch == nil {
		s.runFile(ctx, give)
		return
	}
	// A Reload from the dashboard calls OnUpdate outside Run.
	var fails atomic.Int64
	var gmu sync.Mutex
	c := *s.fetch
	c.OnUpdate = func(b *bundle.Bundle) {
		if n := fails.Swap(0); n > 0 {
			log.Info("shaping source loaded", "failures", n)
		}
		gmu.Lock()
		defer gmu.Unlock()
		give(b)
	}
	c.OnError = func(err error) {
		if fails.Add(1) == 1 {
			log.Warn("shaping source not loaded, retrying", "err", err)
		} else {
			log.Debug("shaping source still not loaded", "err", err)
		}
	}
	f, err := shapebundle.NewFetcher(c)
	if err != nil {
		log.Warn("shaping source not usable", "err", err)
		s.setStatus(func(st *shapebundle.Status) { st.LastError, st.LastErrorAt = err, time.Now() })
		return
	}
	s.fetcher.Store(f)
	// The cached bundle, if any, serves until the first fetch.
	_, _ = f.Load()
	_ = f.Run(ctx)
}

// runFile reads shaping_file again after shapingRefresh, or at the bundle's
// next_change when that is sooner; a failure keeps what is in use and is
// retried with a growing delay.
func (s *shapingSource) runFile(ctx context.Context, give func(*bundle.Bundle)) {
	retry := shapingRetryMin
	var asked chan error // a Reload waiting for this read
	for {
		wait := shapingRefresh
		b, err := shapebundle.ParseFile(s.file, bundle.ParseOptions{})
		if asked != nil {
			asked <- err
			asked = nil
		}
		if err != nil {
			log.Warn("shaping file not loaded, retrying", "file", s.file, "err", err)
			wait, retry = retry, min(retry*2, shapingRetryMax)
			s.setStatus(func(st *shapebundle.Status) { st.LastError, st.LastErrorAt = err, time.Now() })
		} else {
			retry = shapingRetryMin
			s.setStatus(func(st *shapebundle.Status) {
				st.LastSuccess, st.LastError, st.LastErrorAt = time.Now(), nil, time.Time{}
			})
			give(b)
			if d := time.Until(b.NextChange()); d > time.Second && d < wait {
				wait = d
			}
		}
		s.setStatus(func(st *shapebundle.Status) { st.Next = time.Now().Add(wait) })
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			s.setStatus(func(st *shapebundle.Status) { st.Next = time.Time{} })
			return
		case asked = <-s.reread:
			t.Stop()
		case <-t.C:
		}
	}
}
