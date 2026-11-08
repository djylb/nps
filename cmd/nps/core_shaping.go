package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	defer r.Body.Close()
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

// run keeps the bundle current until ctx ends. A failure keeps what is in
// use; one over the size limit or invalid is not given to anybody.
func (s *shapingSource) run(ctx context.Context, set func([]byte)) {
	var last []byte
	for {
		wait := shapingRefresh
		b, err := s.load(ctx)
		if err == nil && len(b) > bundle.DefaultMaxBytes {
			err = errors.New("over the size limit")
		}
		var parsed *bundle.Bundle
		if err == nil {
			parsed, err = bundle.Parse(b, bundle.ParseOptions{})
		}
		switch {
		case err != nil:
			log.Warn("shaping source not loaded", "err", err)
		case !bytes.Equal(b, last):
			last = b
			set(b)
		}
		if parsed != nil {
			if d := time.Until(parsed.NextChange()); d > time.Second && d < wait {
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
