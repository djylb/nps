package index

import (
	"reflect"
	"sort"
	"testing"
)

func sortedInts(values []int) []int {
	out := append([]int(nil), values...)
	sort.Ints(out)
	return out
}

func TestStringIDIndex_BasicOperations(t *testing.T) {
	idx := NewStringIDIndex(4)

	idx.Add("alpha", 1)
	idx.Add("beta", 2)

	if got, ok := idx.Get("alpha"); !ok || got != 1 {
		t.Fatalf("Get(alpha) = (%d, %v), want (1, true)", got, ok)
	}

	idx.Remove("alpha")
	if _, ok := idx.Get("alpha"); ok {
		t.Fatalf("expected alpha to be removed")
	}

	idx.Clear()
	if _, ok := idx.Get("beta"); ok {
		t.Fatalf("expected beta to be removed after Clear")
	}
}

func TestStringIndex_BasicOperations(t *testing.T) {
	idx := NewStringIndex()
	idx.Add("k", "v")

	if got, ok := idx.Get("k"); !ok || got != "v" {
		t.Fatalf("Get(k) = (%q, %v), want (v, true)", got, ok)
	}

	idx.Remove("k")
	if _, ok := idx.Get("k"); ok {
		t.Fatalf("expected key k to be removed")
	}

	idx.Add("a", "1")
	idx.Add("b", "2")
	idx.Clear()
	if _, ok := idx.Get("a"); ok {
		t.Fatalf("expected key a to be removed after Clear")
	}
}

func TestAnyIndexes_Clear(t *testing.T) {
	strIdx := NewAnyStringIndex()
	strIdx.Add("n", 100)
	if got, ok := strIdx.Get("n"); !ok || got.(int) != 100 {
		t.Fatalf("Get(n) = (%v, %v), want (100, true)", got, ok)
	}
	strIdx.Clear()
	if _, ok := strIdx.Get("n"); ok {
		t.Fatalf("expected string key n to be cleared")
	}

	intIdx := NewAnyIntIndex()
	intIdx.Add(7, "value")
	if got, ok := intIdx.Get(7); !ok || got.(string) != "value" {
		t.Fatalf("Get(7) = (%v, %v), want (value, true)", got, ok)
	}
	intIdx.Clear()
	if _, ok := intIdx.Get(7); ok {
		t.Fatalf("expected int key 7 to be cleared")
	}
}

func TestDomainIndex_LookupAndNormalization(t *testing.T) {
	di := NewDomainIndex()

	di.Add("*.Example.COM", 1)
	di.Add("api.example.com", 2)
	di.Add("example.com", 3)
	di.Add("api.example.com", 2) // duplicate should be ignored

	cases := map[string][]int{
		"api.example.com":     {1, 2, 3},
		"deep.api.example.com": {1, 2, 3},
		"example.com":         {1, 3},
		"other.com":           nil,
	}

	for domain, want := range cases {
		got := sortedInts(di.Lookup(domain))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Lookup(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestDomainIndex_RemoveAndDestroy(t *testing.T) {
	di := NewDomainIndex()
	di.Add("example.com", 1)
	di.Add("api.example.com", 2)

	di.Remove("api.example.com", 2)
	if got := sortedInts(di.Lookup("api.example.com")); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("after remove Lookup(api.example.com) = %v, want [1]", got)
	}

	di.Remove("api.example.com", 2) // removing missing pair is no-op
	if got := sortedInts(di.Lookup("api.example.com")); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("after duplicate remove Lookup(api.example.com) = %v, want [1]", got)
	}

	di.Destroy()
	if got := di.Lookup("api.example.com"); len(got) != 0 {
		t.Fatalf("after destroy Lookup(api.example.com) = %v, want empty", got)
	}
}
