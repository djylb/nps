// Package lifecycle tracks the goroutines a component starts, so that
// closing the component can wait until all of them have returned.
package lifecycle

import "sync"

// Group counts goroutines until it is closed; after Close it starts none.
// The zero value is ready to use.
type Group struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// Go runs f in a new goroutine that Wait waits for. It reports false, and
// does not run f, once the group is closed.
func (g *Group) Go(f func()) bool {
	if !g.Enter() {
		return false
	}
	go func() {
		defer g.wg.Done()
		f()
	}()
	return true
}

// Enter counts the calling goroutine, which must call Leave when it is done.
// It reports false, counting nothing, once the group is closed.
func (g *Group) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// Leave ends what a successful Enter counted.
func (g *Group) Leave() { g.wg.Done() }

// Close keeps the group from starting more goroutines. It reports whether
// this call closed it.
func (g *Group) Close() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.closed = true
	return true
}

// Closed reports whether Close was called.
func (g *Group) Closed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// Wait waits for the goroutines counted so far; call it after Close.
func (g *Group) Wait() { g.wg.Wait() }
