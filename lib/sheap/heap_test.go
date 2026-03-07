package sheap

import (
	"container/heap"
	"testing"
)

func TestIntHeapBasicOperations(t *testing.T) {
	h := &IntHeap{3, 1, 2}
	heap.Init(h)

	if got := h.Len(); got != 3 {
		t.Fatalf("unexpected length after init: got %d, want %d", got, 3)
	}

	// Init should build a min-heap; the smallest element should be popped first.
	if got := heap.Pop(h).(int64); got != 1 {
		t.Fatalf("unexpected first pop value: got %d, want %d", got, 1)
	}

	heap.Push(h, int64(0))
	heap.Push(h, int64(4))

	want := []int64{0, 2, 3, 4}
	for i, expected := range want {
		if got := heap.Pop(h).(int64); got != expected {
			t.Fatalf("unexpected value at pop %d: got %d, want %d", i, got, expected)
		}
	}

	if h.Len() != 0 {
		t.Fatalf("heap should be empty after all pops, got len=%d", h.Len())
	}
}

func TestIntHeapSwapAndLess(t *testing.T) {
	h := IntHeap{10, 5}

	if !h.Less(1, 0) {
		t.Fatalf("Less should report index 1 smaller than index 0")
	}

	h.Swap(0, 1)
	if h[0] != 5 || h[1] != 10 {
		t.Fatalf("unexpected values after swap: got %v", []int64(h))
	}
}
