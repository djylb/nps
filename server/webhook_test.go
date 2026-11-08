package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/djylb/slogx"
)

func TestBusDataIsPlainJSON(t *testing.T) {
	r := &slogx.Record{Level: slog.LevelWarn, Module: "bridge", Msg: "m", Attrs: []slog.Attr{
		slog.Any("err", errors.New("boom")), slog.Int("src_port", 5), slog.Any("vkey", slogx.Redacted("k")),
		slog.Time("until", time.Unix(0, 0)), slog.Any("addr", &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 9}),
	}}
	b, err := json.Marshal(busData(r))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"addr":"1.2.3.4:9","err":"boom","level":"WARN","module":"bridge","msg":"m","src_port":5,"until":"1970-01-01T00:00:00Z","vkey":"[REDACTED]"}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
}
