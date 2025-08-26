package rate

import (
	"sync"
	"sync/atomic"
	"time"
)

type Rate struct {
	bucketSize        int64
	bucketSurplusSize int64
	bucketAddSize     int64
	NowRate           int64

	mu       sync.Mutex
	stopChan chan struct{}
	running  bool
}

func NewRate(addSize int64) *Rate {
	return &Rate{
		bucketSize:        addSize * 2,
		bucketSurplusSize: 0,
		bucketAddSize:     addSize,
		stopChan:          make(chan struct{}),
	}
}

// Start 启动回桶
func (s *Rate) Start() {
	s.mu.Lock()
	if !s.running {
		s.running = true
		s.stopChan = make(chan struct{})
		go s.session()
	}
	s.mu.Unlock()
}

func (s *Rate) add(size int64) {
	if res := s.bucketSize - s.bucketSurplusSize; res < s.bucketAddSize {
		atomic.AddInt64(&s.bucketSurplusSize, res)
		return
	}
	atomic.AddInt64(&s.bucketSurplusSize, size)
}

// ReturnBucket 回桶
func (s *Rate) ReturnBucket(size int64) {
	s.add(size)
}

// Stop 停止回桶
func (s *Rate) Stop() {
	s.mu.Lock()
	if s.running {
		close(s.stopChan)
		s.running = false
	}
	s.mu.Unlock()
}

func (s *Rate) Get(size int64) {
	if s.bucketSurplusSize >= size {
		atomic.AddInt64(&s.bucketSurplusSize, -size)
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if s.bucketSurplusSize >= size {
				atomic.AddInt64(&s.bucketSurplusSize, -size)
				return
			}
		case <-s.stopChan:
			return
		}
	}
}

func (s *Rate) session() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rs := s.bucketAddSize - s.bucketSurplusSize
			if rs > 0 {
				atomic.StoreInt64(&s.NowRate, rs)
			} else {
				atomic.StoreInt64(&s.NowRate, s.bucketSize-s.bucketSurplusSize)
			}
			s.add(s.bucketAddSize)
		case <-s.stopChan:
			return
		}
	}
}
