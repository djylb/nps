package pool

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type Pool[T comparable] struct {
	rr   atomic.Uint64
	head uint64
	mu   sync.RWMutex
	list []T
	idx  map[T]int
}

func New[T comparable]() *Pool[T] {
	return &Pool[T]{idx: make(map[T]int)}
}

func (p *Pool[T]) Has(v T) bool {
	p.mu.RLock()
	_, ok := p.idx[v]
	p.mu.RUnlock()
	return ok
}

func (p *Pool[T]) Push(v T) { p.add(v) }
func (p *Pool[T]) Add(v T)  { p.add(v) }

func (p *Pool[T]) add(v T) {
	p.mu.Lock()
	if _, exists := p.idx[v]; !exists {
		p.list = append(p.list, v)
		p.idx[v] = len(p.list) - 1
	}
	p.mu.Unlock()
}

func (p *Pool[T]) Remove(v T) {
	p.mu.Lock()
	if i, ok := p.idx[v]; ok {
		p.removeAt(i)
		if len(p.list) == 0 {
			p.head = 0
		}
	}
	p.mu.Unlock()
}

func (p *Pool[T]) Pop() (v T, ok bool) {
	p.mu.Lock()
	v, ok = p.pop()
	p.mu.Unlock()
	return
}

func (p *Pool[T]) pop() (v T, ok bool) {
	if n := len(p.list); n > 0 {
		v = p.list[n-1]
		ok = true
		p.removeAt(n - 1)
		if len(p.list) == 0 {
			p.head = 0
		}
	}
	return
}

func (p *Pool[T]) Peek() (v T, ok bool) {
	p.mu.RLock()
	if n := len(p.list); n > 0 {
		v, ok = p.list[n-1], true
	}
	p.mu.RUnlock()
	return
}

func (p *Pool[T]) Dequeue() (v T, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.list)
	if n == 0 {
		return
	}
	head := int(p.head)

	idx := head % n
	v, ok = p.list[idx], true

	p.removeAt(idx)

	if n <= 2 {
		p.head = 0
		return
	}

	last := uint64(n - 2)

	if p.head >= last {
		p.head = last
	} else {
		p.head++
	}

	return
}

func (p *Pool[T]) Front() (v T, ok bool) {
	p.mu.RLock()
	if n := len(p.list); n > 0 {
		i := int(p.head % uint64(n))
		v, ok = p.list[i], true
	}
	p.mu.RUnlock()
	return
}

func (p *Pool[T]) Next() (v T, ok bool) { // Round-Robin
	p.mu.RLock()
	if n := len(p.list); n > 0 {
		i := p.rr.Add(1) - 1
		if i > 1<<60 {
			p.rr.Store(0)
			i = 0
		}
		v, ok = p.list[i%uint64(n)], true
	}
	p.mu.RUnlock()
	return
}

func (p *Pool[T]) Random() (v T, ok bool) {
	p.mu.RLock()
	if n := len(p.list); n > 0 {
		v, ok = p.list[rand.Intn(n)], true
	}
	p.mu.RUnlock()
	return
}

func (p *Pool[T]) Size() int {
	p.mu.RLock()
	n := len(p.list)
	p.mu.RUnlock()
	return n
}

func (p *Pool[T]) Range(fn func(T) bool) {
	p.mu.RLock()
	snap := append(make([]T, 0, len(p.list)), p.list...)
	p.mu.RUnlock()

	for _, v := range snap {
		if !fn(v) {
			break
		}
	}
}

func (p *Pool[T]) Clear(clean func(T)) {
	if clean == nil {
		p.mu.Lock()
		p.list = nil
		p.idx = make(map[T]int)
		p.rr.Store(0)
		p.head = 0
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	snap := p.list
	p.list = nil
	p.idx = make(map[T]int)
	p.rr.Store(0)
	p.head = 0
	p.mu.Unlock()

	for _, v := range snap {
		clean(v)
	}
}

func (p *Pool[T]) removeAt(i int) {
	last := len(p.list) - 1
	v := p.list[i]
	if i != last {
		p.list[i] = p.list[last]
		p.idx[p.list[i]] = i
	}
	p.list = p.list[:last]
	delete(p.idx, v)

	if last <= 1 {
		p.head = 0
		return
	}

	lastIdx := uint64(last - 1)

	if p.head > lastIdx {
		p.head = lastIdx
	}
	return
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
