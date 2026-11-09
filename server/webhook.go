package server

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/nps/lib/model"
	"github.com/djylb/slogx"

	"github.com/djylb/nps/lib/event"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/webhook"
)

// EventTunnelsReport is the type of the periodic full report.
var EventTunnelsReport = logevent.TunnelsReport.Name

// TunnelState is a tunnel in the full report.
type TunnelState struct {
	model.TunnelInfo
	Running      bool  `json:"running"`
	ClientOnline bool  `json:"client_online"`
	NowConn      int32 `json:"now_conn"`
	InletFlow    int64 `json:"inlet_flow"`
	ExportFlow   int64 `json:"export_flow"`
}

// HostState is a domain entry in the full report.
type HostState struct {
	model.HostInfo
	ClientOnline bool  `json:"client_online"`
	NowConn      int32 `json:"now_conn"`
	InletFlow    int64 `json:"inlet_flow"`
	ExportFlow   int64 `json:"export_flow"`
}

// TunnelsReport lets a receiver correct what it missed of the events.
type TunnelsReport struct {
	Tunnels []TunnelState `json:"tunnels"`
	Hosts   []HostState   `json:"hosts"`
}

type webhookState struct {
	sync.Mutex
	cfg    servercfg.WebhookConfig
	bus    *event.Bus
	sender *webhook.Sender
	stop   chan struct{}
	done   chan struct{}
	detach func() // the log route that feeds bus

}

// ApplyWebhook makes the webhook run as cfg says; it restarts it, discarding
// what is queued, only if cfg changed. The hub exists only while a webhook is
// configured, so entry changes cost nothing otherwise.
func (s *Server) ApplyWebhook(cfg servercfg.WebhookConfig) {
	w := &s.webhook
	w.Lock()
	defer w.Unlock()
	if cfg == w.cfg {
		return
	}
	if w.bus != nil {
		w.detach()
		s.db.SetEventBus(nil)
		close(w.stop)
		<-w.done
		w.sender.Close()
		w.bus.Close()
		w.bus, w.sender = nil, nil
	}
	w.cfg = cfg
	if cfg.URL == "" {
		return
	}
	if cfg.Secret == "" {
		log.Warn("webhook deliveries are not signed", "reason", "webhook_secret is empty")
	}
	w.bus = event.NewBus()
	w.sender = webhook.Start(webhook.Config{
		URL:     cfg.URL,
		Secret:  cfg.Secret,
		Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		Retries: cfg.Retries,
	}, w.bus.Subscribe(cfg.QueueSize))
	s.db.SetEventBus(w.bus)
	if detach, err := slogx.Default.Attach(busRoute(w.bus)); err == nil {
		w.detach = detach
	} else {
		w.detach = func() {}
	}
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	go s.reportTunnels(w.bus, time.Duration(cfg.ReportInterval)*time.Second, w.stop, w.done)
}

func (s *Server) reportTunnels(bus *event.Bus, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	if interval <= 0 {
		<-stop
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bus.Publish(EventTunnelsReport, s.BuildTunnelsReport())
		case <-stop:
			return
		}
	}
}

// BuildTunnelsReport snapshots all tunnels and domain entries, by id.
func (s *Server) BuildTunnelsReport() TunnelsReport {
	r := TunnelsReport{Tunnels: []TunnelState{}, Hosts: []HostState{}}
	online := func(c *model.Client) bool { return c != nil && s.bridge.Connected(c.Id) }
	s.db.JsonDb.Tasks.Range(func(_, v any) bool {
		t := v.(*model.Tunnel)
		running := s.TunnelRunning(t.Id)
		flow := t.Flow.Values()
		r.Tunnels = append(r.Tunnels, TunnelState{
			TunnelInfo: model.NewTunnelInfo(t), Running: running, ClientOnline: online(t.Client),
			NowConn: t.CurrentConnections(), InletFlow: flow.InletFlow, ExportFlow: flow.ExportFlow,
		})
		return true
	})
	s.db.JsonDb.Hosts.Range(func(_, v any) bool {
		h := v.(*model.Host)
		flow := h.Flow.Values()
		r.Hosts = append(r.Hosts, HostState{
			HostInfo: model.NewHostInfo(h), ClientOnline: online(h.Client),
			NowConn: h.CurrentConnections(), InletFlow: flow.InletFlow, ExportFlow: flow.ExportFlow,
		})
		return true
	})
	sort.Slice(r.Tunnels, func(i, j int) bool { return r.Tunnels[i].ID < r.Tunnels[j].ID })
	sort.Slice(r.Hosts, func(i, j int) bool { return r.Hosts[i].ID < r.Hosts[j].ID })
	return r
}

// busRoute publishes the bus events of the log (logevent, Bus) to bus,
// their fields as the data.
func busRoute(bus *event.Bus) slogx.Route {
	return slogx.Route{Name: "webhook", Match: slogx.MatchBus, Func: func(r *slogx.Record) {
		bus.Publish(r.Def.Name, busData(r))
	}}
}

// busData is the data of a log event on the bus: its fields, values as JSON
// can carry them (errors and other values as their text), plus level,
// module and msg. Secrets stay [REDACTED] whatever log_secrets says.
func busData(r *slogx.Record) map[string]any {
	data := make(map[string]any, len(r.Attrs)+3)
	data["level"] = slogx.LevelName(r.Level)
	if r.Module != "" {
		data["module"] = r.Module
	}
	if r.Msg != "" {
		data["msg"] = r.Msg
	}
	for _, a := range r.Attrs {
		data[a.Key] = busValue(a.Value.Resolve())
	}
	return data
}

func busValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().Milliseconds()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339)
	default:
		return busAny(v.Any())
	}
}

// busAny is the event bus value of what slog holds as any.
func busAny(a any) any {
	switch x := a.(type) {
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	case nil:
		return nil
	default:
		return fmt.Sprint(x)
	}
}
