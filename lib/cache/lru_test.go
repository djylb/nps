package cache

import (
	"reflect"
	"testing"
)

func TestCacheAddGetEvictionAndOnEvicted(t *testing.T) {
	c := New(2)
	var evicted []string
	c.OnEvicted = func(key Key, _ interface{}) {
		evicted = append(evicted, key.(string))
	}

	c.Add("a", 1)
	c.Add("b", 2)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected key a to exist")
	}
	c.Add("c", 3)

	if c.Len() != 2 {
		t.Fatalf("expected cache len 2, got %d", c.Len())
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected key b to be evicted")
	}
	if v, ok := c.Get("a"); !ok || v.(int) != 1 {
		t.Fatalf("expected key a to remain with value 1, got %v, ok=%v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v.(int) != 3 {
		t.Fatalf("expected key c to exist with value 3, got %v, ok=%v", v, ok)
	}

	if !reflect.DeepEqual(evicted, []string{"b"}) {
		t.Fatalf("unexpected evicted keys: %#v", evicted)
	}
}

func TestCacheRemoveAndClearCallOnEvicted(t *testing.T) {
	c := New(0)
	var evicted []string
	c.OnEvicted = func(key Key, _ interface{}) {
		evicted = append(evicted, key.(string))
	}

	c.Add("x", 1)
	c.Add("y", 2)
	c.Remove("x")
	c.Clear()

	if len(evicted) != 2 {
		t.Fatalf("expected 2 evicted callbacks, got %d (%#v)", len(evicted), evicted)
	}
	if !(contains(evicted, "x") && contains(evicted, "y")) {
		t.Fatalf("expected evicted keys x and y, got %#v", evicted)
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
