package mux

import (
	"errors"
	"math"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitForStack polls all goroutine stacks until one contains every marker.
func waitForStack(t *testing.T, markers ...string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			ok := true
			for _, m := range markers {
				if !strings.Contains(g, m) {
					ok = false
					break
				}
			}
			if ok {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no goroutine reached %v", markers)
}

func newTestPack() *muxPackager {
	pack := muxPack.Get()
	_ = pack.Set(muxNewMsg, 1, []byte("x"))
	return pack
}

// The consumer pops while holding cond.L; crossing the low water mark from
// that pop must not need cond.L again.
func TestPriorityQueuePopAcrossLowWaterWhileWaiting(t *testing.T) {
	var q priorityQueue
	q.New()
	got := make(chan *muxPackager, 1)
	go func() { got <- q.Pop() }()
	waitForStack(t, "(*priorityQueue).Pop", "sync.(*Cond).Wait")

	// enqueue exactly writeQueueLowWater frames without waking the consumer,
	// then wake it once: its first pop crosses the low water mark
	for i := uint64(0); i < writeQueueLowWater; i++ {
		atomic.AddUint64(&q.length, 1)
		q.push(newTestPack())
	}
	q.cond.L.Lock()
	q.cond.Signal()
	q.cond.L.Unlock()

	select {
	case pack := <-got:
		if pack == nil {
			t.Fatal("Pop() = nil")
		}
		muxPack.Put(pack)
	case <-time.After(5 * time.Second):
		t.Fatal("Pop() deadlocked when crossing the low water mark")
	}
	if l := q.Len(); l != writeQueueLowWater-1 {
		t.Fatalf("Len() = %d, want %d", l, writeQueueLowWater-1)
	}
	q.Stop()
	for q.drainPop() != nil {
	}
}

// newBackpressuredConn returns a stream on a mux without a write session
// whose write queue sits at the high water mark.
func newBackpressuredConn(t *testing.T) (*Mux, *Conn) {
	t.Helper()
	m := &Mux{closeChan: make(chan struct{}), connMap: NewConnMap()}
	m.writeQueue.New()
	m.newConnQueue.New()
	for i := uint64(0); i < writeQueueHighWater; i++ {
		m.writeQueue.Push(newTestPack())
	}
	c := NewConn(1, m)
	m.connMap.Set(c.connId, c)
	t.Cleanup(func() {
		close(m.closeChan)
		m.release()
	})
	return m, c
}

func startBlockedWrite(t *testing.T, c *Conn) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		n, err := c.Write([]byte("data"))
		if err != nil && n != 0 {
			err = errors.New("partial write reported for a frame that was not queued")
		}
		errCh <- err
	}()
	waitForStack(t, "(*Mux).applyWriteBackpressure")
	return errCh
}

func TestBackpressureWaitEndsOnWriteDeadline(t *testing.T) {
	m, c := newBackpressuredConn(t)
	errCh := startBlockedWrite(t, c)
	_ = c.SetWriteDeadline(time.Now())
	select {
	case err := <-errCh:
		requireTimeout(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("SetWriteDeadline did not wake a writer blocked on backpressure")
	}
	// the failed wait consumed no send window
	if _, send, _ := c.sendWindow.unpack(atomic.LoadUint64(&c.sendWindow.maxSizeDone)); send != 0 {
		t.Fatalf("send window used = %d, want 0", send)
	}

	// draining below low water lets the next write through
	_ = c.SetWriteDeadline(time.Time{})
	for m.writeQueue.Len() >= writeQueueLowWater {
		muxPack.Put(m.writeQueue.TryPop())
	}
	if _, err := c.Write([]byte("data")); err != nil {
		t.Fatalf("Write() after drain error = %v", err)
	}
}

func TestBackpressureWaitEndsOnStreamClose(t *testing.T) {
	_, c := newBackpressuredConn(t)
	errCh := startBlockedWrite(t, c)
	_ = c.Close()
	select {
	case err := <-errCh:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Write() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake a writer blocked on backpressure")
	}
}

func TestBackpressureWaitEndsOnLowWater(t *testing.T) {
	m, c := newBackpressuredConn(t)
	errCh := startBlockedWrite(t, c)
	for m.writeQueue.Len() >= writeQueueLowWater {
		muxPack.Put(m.writeQueue.TryPop())
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer not woken when the queue drained below low water")
	}
}

func TestWriteWindowClosedErrorIsNetErrClosed(t *testing.T) {
	if !errors.Is(errWriteWindowClosed, net.ErrClosed) {
		t.Fatal("errWriteWindowClosed does not match net.ErrClosed")
	}
	if errWriteWindowClosed.Error() != "conn.writeWindow: window closed" {
		t.Fatalf("message = %q", errWriteWindowClosed.Error())
	}
}

func TestDeadlineNanoClampsOutOfRange(t *testing.T) {
	if got := deadlineNano(time.Time{}); got != 0 {
		t.Fatalf("zero time = %d, want 0", got)
	}
	if got := deadlineNano(time.Unix(0, 0)); got != 1 {
		t.Fatalf("unix epoch = %d, want 1", got)
	}
	if got := deadlineNano(time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC)); got != 1 {
		t.Fatalf("far past = %d, want 1", got)
	}
	if got := deadlineNano(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)); got != math.MaxInt64 {
		t.Fatalf("far future = %d, want MaxInt64", got)
	}
	if !deadlinePassed(deadlineNano(time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC))) {
		t.Fatal("far past deadline not reported as passed")
	}
	if deadlinePassed(deadlineNano(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC))) {
		t.Fatal("far future deadline reported as passed")
	}
	if _, _, expired := deadlineTimer(math.MaxInt64); expired {
		t.Fatal("far future deadline timer expired")
	}
}
