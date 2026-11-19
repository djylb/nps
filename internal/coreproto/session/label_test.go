package session

import (
	"context"
	"testing"

	"github.com/zmuxio/zmux-go"
)

func TestHTTPLabel(t *testing.T) {
	at := func(tls bool, major int) context.Context {
		return context.WithValue(context.Background(), httpKey{}, httpReq{tls: tls, major: major})
	}
	for _, c := range []struct {
		ctx  context.Context
		kind zmux.CarrierKind
		want string
	}{
		{at(false, 1), zmux.CarrierWebSocket, "ws"},
		{at(true, 2), zmux.CarrierWebSocket, "wss h2"},
		{at(true, 2), zmux.CarrierStream, "https h2 streaming"},
		{at(false, 1), zmux.CarrierSplit, "http http/1.1 split"},
		{at(true, 1), zmux.CarrierExchange, "https http/1.1 exchange"},
		{at(true, 3), zmux.CarrierHTTP3, "https h3 streaming"},
		{context.Background(), zmux.CarrierStream, "http"},
		{at(true, 1), zmux.CarrierQUIC, "quic"},
	} {
		if got := httpLabel(c.ctx, zmux.PipeIO{Carrier: c.kind}); got != c.want {
			t.Errorf("%v: %q, want %q", c.kind, got, c.want)
		}
	}
	for _, c := range []struct {
		ctx  context.Context
		kind zmux.CarrierKind
		want string
	}{
		{at(true, 2), zmux.CarrierStream, "https"},
		{at(false, 1), zmux.CarrierSplit, "http"},
		{at(true, 2), zmux.CarrierWebSocket, "wss"},
		{at(true, 3), zmux.CarrierHTTP3, "h3"},
		{context.Background(), zmux.CarrierStream, "http"},
	} {
		if got := shortLabel(c.ctx, zmux.PipeIO{Carrier: c.kind}); got != c.want {
			t.Errorf("short %v: %q, want %q", c.kind, got, c.want)
		}
	}
}
