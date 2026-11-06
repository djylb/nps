package mux

import "testing"

func TestWindowBufferPoolPutRejectsUnexpectedCapacity(t *testing.T) {
	p := newWindowBufferPool()

	assertNoPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s panicked: %v", name, r)
			}
		}()
		fn()
	}

	assertNoPanic("small buffer", func() {
		p.Put(make([]byte, poolSizeWindowBuffer/2))
	})

	assertNoPanic("large buffer", func() {
		p.Put(make([]byte, poolSizeWindowBuffer*2))
	})

	buf := p.Get()
	if len(buf) != poolSizeWindowBuffer {
		t.Fatalf("unexpected len: got %d want %d", len(buf), poolSizeWindowBuffer)
	}
	if cap(buf) != poolSizeWindowBuffer {
		t.Fatalf("unexpected cap: got %d want %d", cap(buf), poolSizeWindowBuffer)
	}
}
