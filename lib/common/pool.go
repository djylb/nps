package common

import (
	"sync"
)

const (
	PoolSize      = 64 << 10
	PoolSizeSmall = 255
	PoolSizeUdp   = 1472 + 200
	PoolSizeCopy  = 32 << 10
)

func normalizeForPut(buf []byte, size int) ([]byte, bool) {
	if cap(buf) != size {
		return nil, false
	}
	return buf[:size:size], true
}

type bytePool64K struct {
	pool sync.Pool
}

func newBytePool64K() *bytePool64K {
	return &bytePool64K{
		pool: sync.Pool{
			New: func() any {
				return new([PoolSize]byte)
			},
		},
	}
}

func (p *bytePool64K) Get() []byte {
	return p.pool.Get().(*[PoolSize]byte)[:]
}

func (p *bytePool64K) Put(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSize)
	if !ok {
		return
	}
	p.pool.Put((*[PoolSize]byte)(b))
}

func (p *bytePool64K) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSize)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[PoolSize]byte)(b))
}

type bytePoolUDP struct {
	pool sync.Pool
}

func newBytePoolUDP() *bytePoolUDP {
	return &bytePoolUDP{
		pool: sync.Pool{
			New: func() any {
				return new([PoolSizeUdp]byte)
			},
		},
	}
}

func (p *bytePoolUDP) Get() []byte {
	return p.pool.Get().(*[PoolSizeUdp]byte)[:]
}

func (p *bytePoolUDP) Put(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeUdp)
	if !ok {
		return
	}
	p.pool.Put((*[PoolSizeUdp]byte)(b))
}

func (p *bytePoolUDP) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeUdp)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[PoolSizeUdp]byte)(b))
}

type bytePoolSmall struct {
	pool sync.Pool
}

func newBytePoolSmall() *bytePoolSmall {
	return &bytePoolSmall{
		pool: sync.Pool{
			New: func() any {
				return new([PoolSizeSmall]byte)
			},
		},
	}
}

func (p *bytePoolSmall) Get() []byte {
	return p.pool.Get().(*[PoolSizeSmall]byte)[:]
}

func (p *bytePoolSmall) Put(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeSmall)
	if !ok {
		return
	}
	p.pool.Put((*[PoolSizeSmall]byte)(b))
}

func (p *bytePoolSmall) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeSmall)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[PoolSizeSmall]byte)(b))
}

type bytePoolCopy struct {
	pool sync.Pool
}

func newBytePoolCopy() *bytePoolCopy {
	return &bytePoolCopy{
		pool: sync.Pool{
			New: func() any {
				return new([PoolSizeCopy]byte)
			},
		},
	}
}

func (p *bytePoolCopy) Get() []byte {
	return p.pool.Get().(*[PoolSizeCopy]byte)[:]
}

func (p *bytePoolCopy) Put(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeCopy)
	if !ok {
		return
	}
	p.pool.Put((*[PoolSizeCopy]byte)(b))
}

func (p *bytePoolCopy) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, PoolSizeCopy)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[PoolSizeCopy]byte)(b))
}

var (
	BufPool      = newBytePool64K()
	BufPoolUdp   = newBytePoolUDP()
	BufPoolSmall = newBytePoolSmall()
	BufPoolCopy  = newBytePoolCopy()
)
