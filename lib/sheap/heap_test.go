package sheap

import (
	"container/heap"
	"reflect"
	"testing"
)

func TestIntHeap_MinOrderAndPushPop(t *testing.T) {
	h := &IntHeap{5, 1, 3}
	heap.Init(h)

	heap.Push(h, int64(2))
	heap.Push(h, int64(4))

	var got []int64
	for h.Len() > 0 {
		got = append(got, heap.Pop(h).(int64))
	}

	want := []int64{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pop order, got=%v want=%v", got, want)
	}
}

func TestIntHeap_SwapLessAndLen(t *testing.T) {
	h := IntHeap{10, 20}

	if h.Len() != 2 {
		t.Fatalf("unexpected Len(): %d", h.Len())
	}
	if !h.Less(0, 1) {
		t.Fatalf("Less(0,1) should be true for 10 < 20")
	}

	h.Swap(0, 1)
	if h[0] != 20 || h[1] != 10 {
		t.Fatalf("Swap did not swap elements, heap=%v", h)
	}
}
