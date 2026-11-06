package bridge

import (
	"sync"
	"sync/atomic"
	"time"
)

type IdleTimer struct {
	idle    time.Duration
	closeFn func()
	mu      sync.Mutex
	active  int
	t       *time.Timer
	closed  atomic.Uint32
}

func NewIdleTimer(idle time.Duration, closeFn func()) *IdleTimer {
	it := &IdleTimer{idle: idle, closeFn: closeFn}
	it.t = time.AfterFunc(idle, func() {
		shouldClose := false
		it.mu.Lock()
		if it.active == 0 && it.closed.CompareAndSwap(0, 1) {
			shouldClose = true
		}
		it.mu.Unlock()
		if shouldClose {
			closeFn()
		}
	})
	//it.t.Stop()
	return it
}

func (it *IdleTimer) Inc() {
	if it.closed.Load() == 1 {
		return
	}
	it.mu.Lock()
	it.active++
	if it.active == 1 {
		_ = it.t.Stop()
	}
	it.mu.Unlock()
}

func (it *IdleTimer) Dec() {
	if it.closed.Load() == 1 {
		return
	}
	it.mu.Lock()
	if it.active > 0 {
		it.active--
		if it.active == 0 && it.closed.Load() == 0 {
			it.t.Reset(it.idle)
		}
	}
	it.mu.Unlock()
}

func (it *IdleTimer) Stop() {
	if it.closed.CompareAndSwap(0, 1) {
		_ = it.t.Stop()
	}
}
