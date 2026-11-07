package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memOut struct {
	mu    sync.Mutex
	lines []string
}

func (m *memOut) Write(p []byte) error {
	m.mu.Lock()
	m.lines = append(m.lines, string(p))
	m.mu.Unlock()
	return nil
}
func (m *memOut) Close() error { return nil }
func (m *memOut) all() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.lines, "")
}

func withRoute(t *testing.T, c Config, enc Encoder) (*Logs, *memOut) {
	t.Helper()
	o := &memOut{}
	c.Output = "off"
	c.Routes = append(c.Routes, Route{Name: "t", Encoder: enc, Output: o})
	l := New()
	if err := l.Configure(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l, o
}

func TestRaiseExpires(t *testing.T) {
	l, o := withRoute(t, Config{Level: "warn"}, nil)
	m := l.Module("p2p")
	m.Debug("before")
	l.Levels().Raise("p2p", slog.LevelDebug, 50*time.Millisecond)
	m.Debug("during")
	l.Module("bridge").Debug("other module")
	time.Sleep(120 * time.Millisecond)
	m.Debug("after")
	got := o.all()
	if strings.Contains(got, "before") || !strings.Contains(got, "during") || strings.Contains(got, "other module") || strings.Contains(got, "after") {
		t.Fatalf("got %q", got)
	}
	if s := l.Levels().String(); s != "warn" {
		t.Fatalf("levels %q", s)
	}
}

func TestLevelsHTTP(t *testing.T) {
	l := New()
	defer l.Close()
	srv := httptest.NewServer(l.Levels())
	defer srv.Close()
	put := func(body string) int {
		req, _ := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := put(`{"level":"info,p2p=debug"}`); c != 200 {
		t.Fatal(c)
	}
	if c := put(`{"module":"web","level":"trace","ttl":"1m"}`); c != 200 {
		t.Fatal(c)
	}
	if c := put(`{"level":"loud"}`); c != 400 {
		t.Fatal(c)
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Level  string
		Raises map[string]string
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	if st.Level != "info,p2p=debug" || !strings.HasPrefix(st.Raises["web"], "trace until ") {
		t.Fatalf("state %+v", st)
	}
	if l.Levels().Of("web") != LevelTrace {
		t.Fatal("raise not applied")
	}
}

func TestSamplingPlainLines(t *testing.T) {
	l, o := withRoute(t, Config{Sample: &Sampling{First: 2, Thereafter: 3, Tick: time.Hour}}, nil)
	m := l.Module("x")
	for range 10 {
		m.Info("same")
	}
	// lines 1, 2, then 5 and 8 (every third after the first two)
	if n := strings.Count(o.all(), "msg=same"); n != 4 {
		t.Fatalf("%d lines: %q", n, o.all())
	}
	if !strings.Contains(o.all(), "suppressed=2") {
		t.Fatalf("no suppressed count: %q", o.all())
	}
}

func TestJSONValidAndTyped(t *testing.T) {
	l, o := withRoute(t, Config{}, JSONEncoder{})
	l.Module("web").Info("hi\nthere", "n", 3, "ok", true, "d", 1500*time.Millisecond, "s", "a\"b")
	var m map[string]any
	if err := json.Unmarshal([]byte(o.all()), &m); err != nil {
		t.Fatalf("%v: %q", err, o.all())
	}
	if m["n"] != float64(3) || m["ok"] != true || m["d"] != float64(1500) || m["s"] != `a"b` || m["msg"] != "hi\nthere" || m["module"] != "web" {
		t.Fatalf("decoded %v", m)
	}
}

func TestPrettyHasNoRawControl(t *testing.T) {
	var b []byte
	r := Record{Time: time.Now(), Level: slog.LevelWarn, Module: "m", Msg: "a\x1b[31mb\nc", Attrs: []slog.Attr{slog.String("k", "v\x1b")}}
	b = PrettyEncoder{Color: false}.Encode(b, &r)
	if bytes.Count(b, []byte("\n")) != 1 || bytes.Contains(b, []byte("\x1b")) {
		t.Fatalf("%q", b)
	}
}

func TestSourceAndContext(t *testing.T) {
	type key struct{}
	l, o := withRoute(t, Config{AddSource: true, Context: func(ctx context.Context) []slog.Attr {
		if v, ok := ctx.Value(key{}).(string); ok {
			return []slog.Attr{slog.String("req", v)}
		}
		return nil
	}}, nil)
	ctx := context.WithValue(context.Background(), key{}, "r42")
	l.Module("web").InfoContext(ctx, "handled")
	got := o.all()
	if !strings.Contains(got, "req=r42 msg=handled source=obs/features_test.go:") {
		t.Fatalf("got %q", got)
	}
}

func TestMemoryQueryAndSubscribe(t *testing.T) {
	l := New()
	if err := l.Configure(Config{Output: "off", Level: "debug", Memory: 3}); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	m := l.Memory()
	ctx, cancel := context.WithCancel(context.Background())
	sub := m.Subscribe(ctx, Query{MinLevel: slog.LevelWarn}, 4)
	l.Module("a").Debug("one")
	l.Module("b").Warn("two", "ip", "1.1.1.1")
	l.Module("a").Info("three")
	l.Module("a").Error("four")
	if got := m.Query(Query{AnyLevel: true}); len(got) != 3 || got[0].Msg != "two" {
		t.Fatalf("kept %+v", got)
	}
	if got := m.Query(Query{Modules: []string{"b"}, Text: "1.1.1"}); len(got) != 1 {
		t.Fatalf("query %+v", got)
	}
	if e := <-sub.C(); e.Msg != "two" {
		t.Fatalf("first %+v", e)
	}
	if e := <-sub.C(); e.Msg != "four" {
		t.Fatalf("second %+v", e)
	}
	cancel()
	for range sub.C() {
	}
	if s := l.TakeBuffered(); !strings.Contains(s, "msg=four") || l.TakeBuffered() != "" {
		t.Fatalf("take %q", s)
	}
}

func TestAsyncFlushAndDrop(t *testing.T) {
	o := &memOut{}
	a := Async(o, 1000)
	for range 100 {
		_ = a.Write([]byte("x\n"))
	}
	_ = a.Flush()
	if strings.Count(o.all(), "x") != 100 {
		t.Fatalf("flushed %d", strings.Count(o.all(), "x"))
	}
	_ = a.Close()
	if a.Write([]byte("y")) == nil {
		t.Fatal("write after close")
	}
}

func TestEventCarriesDefWithoutRegistry(t *testing.T) {
	d := &Def{Name: "unregistered_sec", Level: slog.LevelWarn, Security: true}
	var got *Def
	l, _ := withRoute(t, Config{Level: "off", Routes: []Route{{Match: SecurityEvents, Func: func(r *Record) { got = r.Def }}}}, nil)
	Emit(l.Module("m"), d, "src_ip", "1.1.1.1")
	if got != d {
		t.Fatal("security route did not get the event")
	}
}
