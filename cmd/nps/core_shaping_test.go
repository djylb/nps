package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/shapebundle"
)

const testBundle = `{"kinds":{"default":[{}]}}`

// collect runs s until it gives a bundle, and returns it.
func collect(t *testing.T, s *shapingSource) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan []byte, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx, func(b []byte) { got <- b })
	}()
	defer func() { cancel(); <-done }()
	select {
	case b := <-got:
		return b
	case <-ctx.Done():
		t.Fatal("no bundle")
		return nil
	}
}

// shaping_url: the body must carry a valid signature, and a fetched bundle is
// cached for the next start.
func TestShapingSourceURL(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set(shapebundle.SignatureHeader, hex.EncodeToString(ed25519.Sign(priv, []byte(testBundle))))
		_, _ = w.Write([]byte(testBundle))
	}))
	defer srv.Close()
	dir := t.TempDir()
	s := &shapingSource{fetch: &shapebundle.FetcherConfig{URL: srv.URL, Body: []byte("{}"), Token: "tok", PublicKey: pub,
		CacheDir: dir, CacheName: shapingCacheName, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond}}
	if b := collect(t, s); string(b) != testBundle {
		t.Fatalf("got %q", b)
	}
	if c, err := os.ReadFile(filepath.Join(dir, shapingCacheName)); err != nil || string(c) != testBundle {
		t.Fatalf("cache %q %v", c, err)
	}

	// A wrong key refuses the bundle; the cache still serves.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	s.fetch.PublicKey = other
	if b := collect(t, s); string(b) != testBundle {
		t.Fatalf("cached bundle not used: %q", b)
	}
}

func TestShapingSourceFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "shaping.json")
	if err := os.WriteFile(file, []byte(testBundle), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := collect(t, &shapingSource{file: file}); string(b) != testBundle {
		t.Fatalf("got %q", b)
	}
}
