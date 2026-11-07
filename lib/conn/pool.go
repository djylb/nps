package conn

import "sync"

const (
	poolSize     = 64 << 10
	poolSizeCopy = 32 << 10
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
				return new([poolSize]byte)
			},
		},
	}
}

func (p *bytePool64K) Get() []byte {
	return p.pool.Get().(*[poolSize]byte)[:]
}

func (p *bytePool64K) Put(buf []byte) {
	b, ok := normalizeForPut(buf, poolSize)
	if !ok {
		return
	}
	p.pool.Put((*[poolSize]byte)(b))
}

func (p *bytePool64K) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, poolSize)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[poolSize]byte)(b))
}

type bytePoolCopy struct {
	pool sync.Pool
}

func newBytePoolCopy() *bytePoolCopy {
	return &bytePoolCopy{
		pool: sync.Pool{
			New: func() any {
				return new([poolSizeCopy]byte)
			},
		},
	}
}

func (p *bytePoolCopy) Get() []byte {
	return p.pool.Get().(*[poolSizeCopy]byte)[:]
}

func (p *bytePoolCopy) Put(buf []byte) {
	b, ok := normalizeForPut(buf, poolSizeCopy)
	if !ok {
		return
	}
	p.pool.Put((*[poolSizeCopy]byte)(b))
}

func (p *bytePoolCopy) PutZero(buf []byte) {
	b, ok := normalizeForPut(buf, poolSizeCopy)
	if !ok {
		return
	}
	clear(b)
	p.pool.Put((*[poolSizeCopy]byte)(b))
}

// Shared buffer pools: BufPool for packets and relays, BufPoolCopy for the
// copy buffers of reverse proxies.
var (
	BufPool     = newBytePool64K()
	BufPoolCopy = newBytePoolCopy()
)
