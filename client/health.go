package client

import (
	"container/heap"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/sheap"
)

const minDelay = 10 * time.Millisecond

type HealthChecker struct {
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	healths    []*file.Health
	serverConn *conn.Conn
	mu         sync.Mutex
	heap       *sheap.IntHeap
	timer      *time.Timer
	client     *http.Client
}

func NewHealthChecker(parentCtx context.Context, healths []*file.Health, c *conn.Conn) *HealthChecker {
	ctx, cancel := context.WithCancel(parentCtx)
	hq := &sheap.IntHeap{}
	heap.Init(hq)
	now := time.Now()
	for _, hc := range healths {
		if hc.HealthMaxFail > 0 && hc.HealthCheckInterval > 0 && hc.HealthCheckTimeout > 0 {
			//hc.HealthNextTime = now.Add(time.Duration(hc.HealthCheckInterval) * time.Second)
			hc.HealthNextTime = now
			heap.Push(hq, hc.HealthNextTime.Unix())
			hc.HealthMap = make(map[string]int)
		}
	}

	tmr := time.NewTimer(0)
	if !tmr.Stop() {
		<-tmr.C
	}

	return &HealthChecker{
		ctx:        ctx,
		cancel:     cancel,
		healths:    healths,
		serverConn: c,
		heap:       hq,
		timer:      tmr,
		client:     &http.Client{},
	}
}

func (hc *HealthChecker) Start() {
	hc.wg.Add(1)
	go func() {
		defer hc.wg.Done()
		hc.loop()
	}()
}

func (hc *HealthChecker) Stop() {
	hc.cancel()
	hc.mu.Lock()
	stopAndDrain(hc.timer)
	hc.mu.Unlock()
	hc.wg.Wait()
}

func (hc *HealthChecker) Reset() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hq := &sheap.IntHeap{}
	heap.Init(hq)
	now := time.Now()
	for _, h := range hc.healths {
		h.HealthNextTime = now.Add(time.Duration(h.HealthCheckInterval) * time.Second)
		heap.Push(hq, h.HealthNextTime.Unix())
		h.HealthMap = make(map[string]int)
	}
	hc.heap = hq
	stopAndDrain(hc.timer)
}

func (hc *HealthChecker) loop() {
	for {
		hc.mu.Lock()
		if hc.heap.Len() == 0 {
			hc.mu.Unlock()
			logs.Warn("health check list empty, exiting")
			return
		}
		nextUnix := (*hc.heap)[0]
		nextTime := time.Unix(nextUnix, 0)
		delay := time.Until(nextTime)
		if delay < minDelay {
			delay = minDelay
		}
		stopAndDrain(hc.timer)
		hc.timer.Reset(delay)
		hc.mu.Unlock()
		select {
		case <-hc.ctx.Done():
			return
		case <-hc.timer.C:
			hc.runChecks()
		}
	}
}

func (hc *HealthChecker) runChecks() {
	now := time.Now()
	hc.mu.Lock()
	type entry struct {
		h       *file.Health
		oldUnix int64
	}
	var due []entry
	for _, h := range hc.healths {
		if !h.HealthNextTime.After(now) {
			due = append(due, entry{h: h, oldUnix: h.HealthNextTime.Unix()})
		}
	}
	hc.mu.Unlock()
	for _, e := range due {
		hc.doCheck(e.h)
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	newHQ := &sheap.IntHeap{}
	heap.Init(newHQ)

	oldMap := make(map[*file.Health]int64, len(due))
	for _, e := range due {
		oldMap[e.h] = e.oldUnix
	}

	for _, h := range hc.healths {
		if oldUnix, ok := oldMap[h]; ok {
			interval := time.Duration(h.HealthCheckInterval) * time.Second
			missed := (now.Unix()-oldUnix)/int64(interval.Seconds()) + 1
			h.HealthNextTime = time.Unix(oldUnix, 0).Add(time.Duration(missed) * interval)
		}
		heap.Push(newHQ, h.HealthNextTime.Unix())
	}
	hc.heap = newHQ
}

func (hc *HealthChecker) doCheck(h *file.Health) {
	timeout := time.Duration(h.HealthCheckTimeout) * time.Second
	for _, target := range strings.Split(h.HealthCheckTarget, ",") {
		var err error
		switch h.HealthCheckType {
		case "tcp":
			c, errDial := net.DialTimeout("tcp", target, timeout)
			if errDial == nil {
				_ = c.Close()
			} else {
				err = errDial
			}
		case "http", "https":
			// HTTP/HTTPS Check
			scheme := h.HealthCheckType // "http" or "https"
			url := fmt.Sprintf("%s://%s%s", scheme, target, h.HttpHealthUrl)
			ctx, cancel := context.WithTimeout(hc.ctx, timeout)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, getErr := hc.client.Do(req)
			cancel()
			if getErr != nil {
				err = getErr
			} else {
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("unexpected status %d", resp.StatusCode)
				}
				_ = resp.Body.Close()
			}
		default:
			err = fmt.Errorf("unsupported health check type: %s", h.HealthCheckType)
		}
		h.Lock()
		if err != nil {
			h.HealthMap[target]++
			if h.HealthMap[target]%h.HealthMaxFail == 0 {
				_, _ = hc.serverConn.SendHealthInfo(target, "0")
			}
		} else {
			if h.HealthMap[target] >= h.HealthMaxFail {
				_, _ = hc.serverConn.SendHealthInfo(target, "1")
			}
			h.HealthMap[target] = 0
		}
		h.Unlock()
	}
}

func stopAndDrain(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
