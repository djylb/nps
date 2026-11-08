// Package webhook delivers the events of a subscription as signed HTTP POSTs.
//
// Delivery is at least once and in order, from one goroutine: while an event is
// being retried the later ones wait in the subscription's bounded queue, and
// what does not fit there is dropped by the hub. Receivers de-duplicate by the
// event id, which stays the same across retries.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/event"
	"github.com/djylb/slogx"
)

var log = slogx.Module("webhook")

// The headers of a delivery.
const (
	HeaderEventID   = "X-NPS-Event-Id"
	HeaderEventType = "X-NPS-Event-Type"
	HeaderTimestamp = "X-NPS-Timestamp"
	HeaderSignature = "X-NPS-Signature"
)

const (
	defaultTimeout = 5 * time.Second
	defaultBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// Config configures a Sender.
type Config struct {
	URL string
	// Secret signs the deliveries; if empty they carry no signature.
	Secret string
	// Timeout bounds one attempt; zero or less means 5 seconds.
	Timeout time.Duration
	// Retries is how many times a failed attempt is repeated; zero or less
	// means none.
	Retries int
	// Backoff is the wait before the first retry, doubling up to 30 seconds;
	// zero or less means a second.
	Backoff time.Duration
}

// Sign returns the value of HeaderSignature for a body sent at timestamp:
// "sha256=" and the hex HMAC-SHA256 of "<timestamp>.<body>".
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Sender delivers the events of one subscription.
type Sender struct {
	cfg       Config
	sub       *event.Subscription
	client    *http.Client
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	delivered atomic.Uint64
	failed    atomic.Uint64
}

// Start begins delivering what sub receives. The Sender owns sub.
func Start(cfg Config, sub *event.Subscription) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultBackoff
	}
	cfg.Retries = max(cfg.Retries, 0)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sender{
		cfg: cfg,
		sub: sub,
		client: &http.Client{
			Transport: http.DefaultTransport.(*http.Transport).Clone(),
			// A redirected POST could lose its body; it counts as a failure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go s.run()
	return s
}

// Close stops at once: an attempt in flight is cancelled and queued events are
// discarded. Deliveries are not persisted, so they do not survive it.
func (s *Sender) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.sub.Close()
		<-s.done
		s.client.CloseIdleConnections()
	})
}

// Delivered returns how many events a receiver accepted.
func (s *Sender) Delivered() uint64 { return s.delivered.Load() }

// Failed returns how many events were given up on.
func (s *Sender) Failed() uint64 { return s.failed.Load() }

func (s *Sender) run() {
	defer close(s.done)
	var dropped uint64
	for e := range s.sub.C() {
		if s.ctx.Err() != nil {
			return
		}
		if err := s.deliver(e); err != nil && s.ctx.Err() == nil {
			s.failed.Add(1)
			log.Warn("event not delivered", "id", e.ID, "type", e.Type, "err", err)
		}
		if n := s.sub.Dropped(); n != dropped {
			log.Warn("queue full, events dropped", "count", n-dropped)
			dropped = n
		}
	}
}

func (s *Sender) deliver(e event.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wait := s.cfg.Backoff
	for attempt := 0; ; attempt++ {
		retry, err := s.attempt(e, body)
		if err == nil {
			s.delivered.Add(1)
			return nil
		}
		if !retry || attempt >= s.cfg.Retries {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-s.ctx.Done():
			t.Stop()
			return s.ctx.Err()
		}
		wait = min(wait*2, maxBackoff)
	}
}

// attempt posts body once and says whether a failure is worth repeating: the
// receiver refusing the request, other than for being busy, will not change.
func (s *Sender) attempt(e event.Event, body []byte) (retry bool, err error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return false, withoutURL(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "nps-webhook")
	req.Header.Set(HeaderEventID, e.ID)
	req.Header.Set(HeaderEventType, e.Type)
	// Stamped per attempt: a receiver may refuse an old timestamp as a replay.
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set(HeaderTimestamp, timestamp)
	if s.cfg.Secret != "" {
		req.Header.Set(HeaderSignature, Sign(s.cfg.Secret, timestamp, body))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return true, withoutURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.CopyN(io.Discard, resp.Body, 64<<10) // lets the connection be reused
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return false, nil
	case code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests:
		return false, fmt.Errorf("status %d", code)
	default:
		return true, fmt.Errorf("status %d", code)
	}
}

// withoutURL keeps the configured URL, which may hold a token, out of the logs.
func withoutURL(err error) error {
	if u, ok := errors.AsType[*url.Error](err); ok {
		return u.Err
	}
	return err
}
