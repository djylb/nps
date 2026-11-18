package main

import (
	"bytes"
	"context"
	"fmt"
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
}

// newShapingSource returns the source of cfg, nil when none is configured.
func newShapingSource(cfg *servercfg.Snapshot) (*shapingSource, error) {
	n := cfg.Network
	if n.ShapingURL == "" {
		file := runenv.GetPath(n.ShapingFile)
		if !runenv.FileExists(file) {
			return nil, nil
		}
		return &shapingSource{file: file}, nil
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
	var fails int
	c := *s.fetch
	c.OnUpdate = func(b *bundle.Bundle) {
		if fails > 0 {
			log.Info("shaping source loaded", "failures", fails)
			fails = 0
		}
		give(b)
	}
	c.OnError = func(err error) {
		if fails == 0 {
			log.Warn("shaping source not loaded, retrying", "err", err)
		} else {
			log.Debug("shaping source still not loaded", "err", err)
		}
		fails++
	}
	f, err := shapebundle.NewFetcher(c)
	if err != nil {
		log.Warn("shaping source not usable", "err", err)
		return
	}
	// The cached bundle, if any, serves until the first fetch.
	_, _ = f.Load()
	_ = f.Run(ctx)
}

// runFile reads shaping_file again after shapingRefresh, or at the bundle's
// next_change when that is sooner; a failure keeps what is in use and is
// retried with a growing delay.
func (s *shapingSource) runFile(ctx context.Context, give func(*bundle.Bundle)) {
	retry := shapingRetryMin
	for {
		wait := shapingRefresh
		if b, err := shapebundle.ParseFile(s.file, bundle.ParseOptions{}); err != nil {
			log.Warn("shaping file not loaded, retrying", "file", s.file, "err", err)
			wait, retry = retry, min(retry*2, shapingRetryMax)
		} else {
			retry = shapingRetryMin
			give(b)
			if d := time.Until(b.NextChange()); d > time.Second && d < wait {
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
