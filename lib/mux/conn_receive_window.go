package mux

import (
	"errors"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type receiveWindow struct {
	window
	bufQueue *receiveWindowQueue
	element  *listElement
	buffered uint32
	count    int8
	sizeMu   sync.Mutex
	readMu   sync.Mutex
	bw       *writeBandwidth
	priority bool
	once     sync.Once
	// receive window send the current max size and read size to send window
	// means done size actually store the size receive window has read
}

func (Self *receiveWindow) New(mux *Mux) {
	Self.bufQueue = newReceiveWindowQueue()
	Self.element = listEle.Get()
	Self.maxSizeDone = Self.pack(defaultInitialConnWindow, 0, false)
	Self.mux = mux
	Self.window.New()
	Self.bw = newWriteBandwidth()
	Self.priority = false
}

func (Self *receiveWindow) bufferedBytes() uint32 {
	return atomic.LoadUint32(&Self.buffered)
}

func (Self *receiveWindow) reserveBytes(l uint16) {
	if l == 0 {
		return
	}
	atomic.AddUint32(&Self.buffered, uint32(l))
	Self.mux.addSessionReceive(uint32(l))
}

func (Self *receiveWindow) releaseBytes(l uint16) {
	if l == 0 {
		return
	}
	atomic.AddUint32(&Self.buffered, ^(uint32(l) - 1))
	Self.mux.releaseSessionReceive(uint32(l))
}

func (Self *receiveWindow) effectiveMaxSize(maxSize uint32) uint32 {
	if maxSize < defaultInitialConnWindow {
		maxSize = defaultInitialConnWindow
	}
	if limit := Self.mux.maxConnReceiveWindow(); maxSize > limit {
		maxSize = limit
	}
	if sessionLimit := Self.mux.sessionReceiveWindowForConn(Self.bufferedBytes()); maxSize > sessionLimit {
		maxSize = sessionLimit
	}
	return maxSize
}

func (Self *receiveWindow) remainingSize(maxSize uint32, delta uint16) (n uint32) {
	maxSize = Self.effectiveMaxSize(maxSize)
	used := uint64(Self.bufferedBytes()) + uint64(delta)
	if used < uint64(maxSize) {
		n = uint32(uint64(maxSize) - used)
	}
	return
}

func (Self *receiveWindow) calcSize() {
	Self.sizeMu.Lock()
	defer Self.sizeMu.Unlock()
	if Self.count == 0 {
		muxBw := Self.mux.bw.Get()
		connBw := Self.bw.Get()
		latency := math.Float64frombits(atomic.LoadUint64(&Self.mux.latency))
		var n uint32
		if connBw > 0 && muxBw > 0 {
			if connBw > muxBw {
				connBw = muxBw
				Self.bw.GrowRatio()
			}
			n = uint32(latency * (muxBw + connBw))
		}
		if n < defaultInitialConnWindow {
			n = defaultInitialConnWindow
		}
		if n < uint32(float64(maximumSegmentSize*3000)*latency) {
			// latency gain
			// if there are some latency more than 10ms will trigger this gain
			// network pipeline need fill more data that we can measure the max bandwidth
			n = uint32(float64(maximumSegmentSize*3000) * latency)
		}
		for {
			ptrs := atomic.LoadUint64(&Self.maxSizeDone)
			size, read, wait := Self.unpack(ptrs)
			size = Self.effectiveMaxSize(size)
			rem := Self.remainingSize(size, 0)
			ra := float64(rem) / float64(size)
			if ra > 0.8 {
				// low fill window gain
				// if receive window keep low fill, maybe pipeline fill the data, we need a gain
				// less than 20% fill, gain will trigger
				n = uint32(float64(n) * 1.5625 * ra * ra)
			}
			minN := uint32((uint64(size) * 7) / 8)
			if wait && rem == 0 {
				minN = size / 2
			}
			if n < minN {
				n = minN
			}
			// set the minimal size
			if n > 2*size {
				if size == defaultInitialConnWindow {
					// we give more ratio when the initial window size, to reduce the time window grow up
					if n > size*6 {
						n = size * 6
					}
				} else {
					n = 2 * size
					// twice grow
				}
			}
			if connBw > 0 && muxBw > 0 {
				limit := uint32(maximumWindowSize * (connBw / (muxBw + connBw)))
				if n > limit {
					n = limit
				}
			}
			if limit := Self.mux.maxConnReceiveWindow(); n > limit {
				n = limit
			}
			// set the maximum size
			n = Self.effectiveMaxSize(n)
			if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(n, read, wait)) {
				// only change the maxSize
				break
			}
		}
		Self.count = -10
	}
	Self.count += 1
	//return
}

func (Self *receiveWindow) Write(buf []byte, l uint16, part bool, id int32) (err error) {
	if Self.IsClosed() {
		return errors.New("conn.receiveWindow: write on closed window")
	}
	element, err := newListElement(buf, l, part)
	if err != nil {
		return
	}
	Self.calcSize() // calculate the max window size
	var wait bool
	var rawMaxSize, maxSize, read uint32
	var sendUpdate bool
	var sendRead uint32
start:
	ptrs := atomic.LoadUint64(&Self.maxSizeDone)
	rawMaxSize, read, wait = Self.unpack(ptrs)
	maxSize = Self.effectiveMaxSize(rawMaxSize)
	remain := Self.remainingSize(maxSize, l)
	// calculate the remaining window size now, plus the element we will push
	if remain == 0 && !wait {
		wait = true
	}
	sendUpdate = maxSize != rawMaxSize
	if !wait && read > 0 && (read >= maxSize/2 || read >= maximumSegmentSize*4) {
		sendUpdate = true
	}
	nextRead := read
	sendRead = 0
	if sendUpdate {
		nextRead = 0
		sendRead = read
	}
	if !atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, nextRead, wait)) {
		goto start
	}
	Self.reserveBytes(l)
	Self.bufQueue.Push(element)
	if sendUpdate {
		Self.mux.sendInfo(muxMsgSendOk, id, Self.priority, Self.pack(maxSize, sendRead, false))
	}
	return nil
}

func (Self *receiveWindow) Read(p []byte, id int32) (n int, err error) {
	Self.readMu.Lock()
	defer Self.readMu.Unlock()
	if Self.IsClosed() {
		return 0, io.EOF
	}
	Self.bw.StartRead()
	n, err = Self.readFromQueue(p, id)
	Self.bw.SetCopySize(uint16(n))
	return
}

func (Self *receiveWindow) readFromQueue(p []byte, id int32) (n int, err error) {
	pOff := 0
	l := 0
copyData:
	if Self.off == uint32(Self.element.L) {
		listEle.Put(Self.element)
		if Self.IsClosed() {
			return 0, io.EOF
		}
		Self.element, err = Self.bufQueue.Pop()
		Self.off = 0
		if err != nil {
			return
		}
	}
	l = copy(p[pOff:], Self.element.Buf[Self.off:Self.element.L])
	pOff += l
	Self.off += uint32(l)
	n += l
	if Self.off == uint32(Self.element.L) {
		windowBuff.Put(Self.element.Buf)
		Self.releaseBytes(Self.element.L)
		Self.sendStatus(id, Self.element.L)
	}
	if pOff < len(p) && Self.element.Part {
		goto copyData
	}
	return
}

func (Self *receiveWindow) sendStatus(id int32, l uint16) {
	var maxSize, read uint32
	var wait bool
	Self.calcSize()
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		rawMaxSize, curRead, curWait := Self.unpack(ptrs)
		maxSize, read, wait = Self.effectiveMaxSize(rawMaxSize), curRead, curWait
		nextRead := read + uint32(l)
		sendUpdate := maxSize != rawMaxSize
		if nextRead&mask31 < read {
			nextRead = uint32(l)
			sendUpdate = true
		}
		remain := Self.remainingSize(maxSize, 0)
		if wait && remain > 0 || nextRead >= maxSize/2 || nextRead >= maximumSegmentSize*4 || remain == maxSize {
			sendUpdate = true
		}
		if sendUpdate {
			if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, 0, false)) {
				Self.mux.sendInfo(muxMsgSendOk, id, Self.priority, Self.pack(maxSize, nextRead, false))
				break
			}
		} else if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, nextRead, wait)) {
			break
		}
		runtime.Gosched()
	}
}

func (Self *receiveWindow) SetTimeOut(t time.Time) {
	Self.bufQueue.SetTimeOut(t)
}

func (Self *receiveWindow) Stop() {
	Self.once.Do(Self.bufQueue.Stop)
}

func (Self *receiveWindow) CloseWindow() {
	Self.window.CloseWindow()
	Self.Stop()
	Self.release()
}

func (Self *receiveWindow) release() {
	Self.readMu.Lock()
	defer Self.readMu.Unlock()
	for {
		ele := Self.bufQueue.TryPop()
		if ele == nil {
			break
		}
		Self.releaseBytes(ele.L)
		if ele.Buf != nil {
			windowBuff.Put(ele.Buf)
		}
		listEle.Put(ele)
	}
	if Self.element != nil {
		if Self.off < uint32(Self.element.L) {
			Self.releaseBytes(Self.element.L)
		}
		if Self.off < uint32(Self.element.L) && Self.element.Buf != nil {
			windowBuff.Put(Self.element.Buf)
		}
		listEle.Put(Self.element)
		Self.element = nil
	}
}
