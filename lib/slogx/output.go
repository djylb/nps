package slogx

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Output receives encoded lines, one per Write. Write must be safe for
// concurrent use, and must not keep line after it returns: the buffer is
// reused.
type Output interface {
	Write(line []byte) error
	Close() error
}

// Flusher is an Output that holds lines back; Flush writes them out.
type Flusher interface {
	Flush() error
}

// Reopener is an Output backed by a file that an external rotation may
// move away; Reopen opens it again.
type Reopener interface {
	Reopen() error
}

// WriterOutput writes lines to w under a lock; Close closes w only if it is
// an io.Closer other than os.Stdout or os.Stderr.
func WriterOutput(w io.Writer) Output { return &writerOutput{w: w} }

type writerOutput struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *writerOutput) Write(p []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, err := o.w.Write(p)
	return err
}

func (o *writerOutput) Close() error {
	if o.w == os.Stdout || o.w == os.Stderr {
		return nil
	}
	if c, ok := o.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Async returns an Output that hands lines to o on a goroutine of its own
// through a queue of size lines. A line that does not fit is dropped and
// counted rather than blocking the caller. Flush waits until the queue is
// empty; Close flushes and closes o. Both wait for o, so an o that hangs
// hangs them.
func Async(o Output, size int) *AsyncOutput {
	a := &AsyncOutput{out: o, q: make(chan asyncItem, max(size, 1)), done: make(chan struct{})}
	go a.run()
	return a
}

// AsyncOutput is the Output of Async.
type AsyncOutput struct {
	// OnDrop, when set before the first Write, is called from the queue's
	// goroutine once the queue has drained after dropping lines, with how
	// many were dropped since the last call. It may log: the queue has room.
	OnDrop func(dropped uint64)

	out      Output
	q        chan asyncItem
	done     chan struct{}
	dropped  atomic.Uint64
	reported uint64 // run goroutine only
	closeMu  sync.RWMutex
	closed   bool
}

type asyncItem struct {
	line  []byte
	flush chan struct{}
}

func (a *AsyncOutput) run() {
	defer close(a.done)
	for it := range a.q {
		if it.flush != nil {
			if f, ok := a.out.(Flusher); ok {
				_ = f.Flush()
			}
			close(it.flush)
		} else {
			_ = a.out.Write(it.line)
		}
		if len(a.q) == 0 && a.OnDrop != nil {
			if n := a.dropped.Load(); n > a.reported {
				a.OnDrop(n - a.reported)
				a.reported = n
			}
		}
	}
}

// Write queues a copy of p, or drops it when the queue is full.
func (a *AsyncOutput) Write(p []byte) error {
	a.closeMu.RLock()
	defer a.closeMu.RUnlock()
	if a.closed {
		return os.ErrClosed
	}
	select {
	case a.q <- asyncItem{line: append([]byte(nil), p...)}:
	default:
		a.dropped.Add(1)
	}
	return nil
}

// Dropped reports how many lines did not fit in the queue.
func (a *AsyncOutput) Dropped() uint64 { return a.dropped.Load() }

// Flush waits until the lines queued before it are written and o flushed.
func (a *AsyncOutput) Flush() error {
	a.closeMu.RLock()
	if a.closed {
		a.closeMu.RUnlock()
		return nil
	}
	c := make(chan struct{})
	a.q <- asyncItem{flush: c}
	a.closeMu.RUnlock()
	<-c
	return nil
}

// Reopen reopens o when it is a Reopener.
func (a *AsyncOutput) Reopen() error {
	if r, ok := a.out.(Reopener); ok {
		return r.Reopen()
	}
	return nil
}

// Close writes what is queued, then closes o; writes after it fail.
func (a *AsyncOutput) Close() error {
	a.closeMu.Lock()
	if a.closed {
		a.closeMu.Unlock()
		return nil
	}
	a.closed = true
	close(a.q)
	a.closeMu.Unlock()
	<-a.done
	return a.out.Close()
}
