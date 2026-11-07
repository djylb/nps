package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/djylb/nps/lib/slogx"
)

var log = slogx.Module("client")

const fetchTimeout = 15 * time.Second

// RemoteError reports that a URL payload could not be fetched or used, so a
// later attempt may succeed. Revoked means the source answered 410 Gone: the
// configuration it served earlier must not be used any more.
type RemoteError struct {
	Revoked bool
	Err     error
}

func (e *RemoteError) Error() string { return "launch source: " + e.Err.Error() }
func (e *RemoteError) Unwrap() error { return e.Err }

var httpClient = &http.Client{
	Timeout: fetchTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if via[0].URL.Scheme == "https" && !strings.EqualFold(req.URL.Scheme, "https") {
			return errors.New("redirect from https to http")
		}
		return nil
	},
}

func fetch(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", &RemoteError{Err: errors.New("invalid URL")}
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		log.Warn("launch payload fetched over plain http; anyone on the path can read or replace it")
	}
	req.Header.Set("User-Agent", "npc-launch")
	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL may hold an access token
		}
		return "", &RemoteError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &RemoteError{
			Revoked: resp.StatusCode == http.StatusGone,
			Err:     fmt.Errorf("status %s", resp.Status),
		}
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return "", &RemoteError{Err: err}
	}
	return body, nil
}

func readLimited(r io.Reader) (string, error) {
	var b strings.Builder
	n, err := io.Copy(&b, io.LimitReader(r, maxPayloadSize+1))
	if err != nil {
		return "", err
	}
	if n > maxPayloadSize {
		return "", fmt.Errorf("payload is larger than %d bytes", maxPayloadSize)
	}
	return b.String(), nil
}
