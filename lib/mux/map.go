package mux

import (
	"sync"
)

type ConnMap struct {
	cMap map[int32]*Conn
	//closeCh chan struct{}
	sync.RWMutex
}

func NewConnMap() *ConnMap {
	cMap := &ConnMap{
		cMap: make(map[int32]*Conn),
	}
	return cMap
}

func (s *ConnMap) Size() (n int) {
	s.RLock()
	n = len(s.cMap)
	s.RUnlock()
	return
}

func (s *ConnMap) Get(id int32) (*Conn, bool) {
	s.RLock()
	v, ok := s.cMap[id]
	s.RUnlock()
	if ok && v != nil {
		return v, true
	}
	return nil, false
}

func (s *ConnMap) Set(id int32, v *Conn) {
	s.Lock()
	s.cMap[id] = v
	s.Unlock()
}

func (s *ConnMap) Close() {
	// first copy cMap, because conn close will call Delete to trigger dead lock
	var copyMap []*Conn
	s.RLock()
	for _, v := range s.cMap {
		copyMap = append(copyMap, v)
	}
	s.RUnlock()

	// close connections in cMap
	for _, v := range copyMap {
		_ = v.Close() // close all the connections in the mux
	}
}

func (s *ConnMap) Delete(id int32) {
	s.Lock()
	delete(s.cMap, id)
	s.Unlock()
}
