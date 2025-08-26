package index

import "sync"

type StringIDIndex struct {
	mu   sync.RWMutex
	data map[string]int
}

func NewStringIDIndex(initialCapacity ...int) *StringIDIndex {
	var cap0 int
	if len(initialCapacity) > 0 && initialCapacity[0] > 0 {
		cap0 = initialCapacity[0]
	}
	idx := &StringIDIndex{}
	if cap0 > 0 {
		idx.data = make(map[string]int, cap0)
	} else {
		idx.data = make(map[string]int)
	}
	return idx
}

func (idx *StringIDIndex) Add(key string, id int) {
	idx.mu.Lock()
	idx.data[key] = id
	idx.mu.Unlock()
}

func (idx *StringIDIndex) Get(key string) (id int, ok bool) {
	idx.mu.RLock()
	id, ok = idx.data[key]
	idx.mu.RUnlock()
	return
}

func (idx *StringIDIndex) Remove(key string) {
	idx.mu.Lock()
	delete(idx.data, key)
	idx.mu.Unlock()
}

func (idx *StringIDIndex) Clear() {
	idx.mu.Lock()
	idx.data = make(map[string]int)
	idx.mu.Unlock()
}

type StringIndex struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewStringIndex(initialCapacity ...int) *StringIndex {
	var cap0 int
	if len(initialCapacity) > 0 && initialCapacity[0] > 0 {
		cap0 = initialCapacity[0]
	}
	idx := &StringIndex{}
	if cap0 > 0 {
		idx.data = make(map[string]string, cap0)
	} else {
		idx.data = make(map[string]string)
	}
	return idx
}

func (idx *StringIndex) Add(key, value string) {
	idx.mu.Lock()
	idx.data[key] = value
	idx.mu.Unlock()
}

func (idx *StringIndex) Get(key string) (value string, ok bool) {
	idx.mu.RLock()
	value, ok = idx.data[key]
	idx.mu.RUnlock()
	return
}

func (idx *StringIndex) Remove(key string) {
	idx.mu.Lock()
	delete(idx.data, key)
	idx.mu.Unlock()
}

func (idx *StringIndex) Clear() {
	idx.mu.Lock()
	idx.data = make(map[string]string)
	idx.mu.Unlock()
}

type AnyStringIndex struct {
	data sync.Map // map[string]interface{}
}

func NewAnyStringIndex() *AnyStringIndex {
	return &AnyStringIndex{}
}

func (idx *AnyStringIndex) Add(key string, value interface{}) {
	idx.data.Store(key, value)
}

func (idx *AnyStringIndex) Get(key string) (value interface{}, ok bool) {
	return idx.data.Load(key)
}

func (idx *AnyStringIndex) Remove(key string) {
	idx.data.Delete(key)
}

func (idx *AnyStringIndex) Clear() {
	idx.data.Range(func(k, _ interface{}) bool {
		idx.data.Delete(k)
		return true
	})
}

type AnyIntIndex struct {
	data sync.Map // map[int]interface{}
}

func NewAnyIntIndex() *AnyIntIndex {
	return &AnyIntIndex{}
}

func (idx *AnyIntIndex) Add(key int, value interface{}) {
	idx.data.Store(key, value)
}

func (idx *AnyIntIndex) Get(key int) (value interface{}, ok bool) {
	return idx.data.Load(key)
}

func (idx *AnyIntIndex) Remove(key int) {
	idx.data.Delete(key)
}

func (idx *AnyIntIndex) Clear() {
	idx.data.Range(func(k, _ interface{}) bool {
		idx.data.Delete(k)
		return true
	})
}
