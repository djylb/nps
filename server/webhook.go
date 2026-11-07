package server

import (
	"sort"
	"sync"
	"time"

	"github.com/djylb/nps/lib/model"

	"github.com/djylb/nps/lib/event"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/webhook"
)

// EventTunnelsReport is the type of the periodic full report.
const EventTunnelsReport = "tunnels.report"

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

var webhookState struct {
	sync.Mutex
	cfg    servercfg.WebhookConfig
	bus    *event.Bus
	sender *webhook.Sender
	stop   chan struct{}
	done   chan struct{}
}

// ApplyWebhook makes the webhook run as cfg says; it restarts it, discarding
// what is queued, only if cfg changed. The hub exists only while a webhook is
// configured, so entry changes cost nothing otherwise.
func ApplyWebhook(cfg servercfg.WebhookConfig) {
	w := &webhookState
	w.Lock()
	defer w.Unlock()
	if cfg == w.cfg {
		return
	}
	if w.bus != nil {
		file.GetDb().SetEventBus(nil)
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
		logs.Warn("webhook_secret is empty: webhook deliveries are not signed")
	}
	w.bus = event.NewBus()
	w.sender = webhook.Start(webhook.Config{
		URL:     cfg.URL,
		Secret:  cfg.Secret,
		Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		Retries: cfg.Retries,
	}, w.bus.Subscribe(cfg.QueueSize))
	file.GetDb().SetEventBus(w.bus)
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	go reportTunnels(w.bus, time.Duration(cfg.ReportInterval)*time.Second, w.stop, w.done)
}

func reportTunnels(bus *event.Bus, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
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
			bus.Publish(EventTunnelsReport, BuildTunnelsReport())
		case <-stop:
			return
		}
	}
}

// BuildTunnelsReport snapshots all tunnels and domain entries, by id.
func BuildTunnelsReport() TunnelsReport {
	r := TunnelsReport{Tunnels: []TunnelState{}, Hosts: []HostState{}}
	online := func(c *model.Client) bool { return c != nil && Bridge != nil && Bridge.Connected(c.Id) }
	file.GetDb().JsonDb.Tasks.Range(func(_, v any) bool {
		t := v.(*model.Tunnel)
		_, running := RunList.Load(t.Id)
		flow := t.Flow.Values()
		r.Tunnels = append(r.Tunnels, TunnelState{
			TunnelInfo: model.NewTunnelInfo(t), Running: running, ClientOnline: online(t.Client),
			NowConn: t.CurrentConnections(), InletFlow: flow.InletFlow, ExportFlow: flow.ExportFlow,
		})
		return true
	})
	file.GetDb().JsonDb.Hosts.Range(func(_, v any) bool {
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
