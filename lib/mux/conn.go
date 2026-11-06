package mux

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/logs"
)

type Conn struct {
	net.Conn
	connStatusOkCh   chan struct{}
	connStatusFailCh chan struct{}
	connId           int32
	priority         atomic.Bool // set once a stream turns out to carry signalling
	isClose          uint32
	closingFlag      uint32 // closing Conn flag
	receiveWindow    *receiveWindow
	sendWindow       *sendWindow
	once             sync.Once
}

func NewConn(connId int32, mux *Mux) *Conn {
	c := &Conn{
		connStatusOkCh:   make(chan struct{}, 1),
		connStatusFailCh: make(chan struct{}, 1),
		connId:           connId,
		receiveWindow:    new(receiveWindow),
		sendWindow:       new(sendWindow),
		once:             sync.Once{},
	}
	c.receiveWindow.New(mux)
	c.sendWindow.New(mux)
	return c
}

func (s *Conn) SetPriority() {
	s.priority.Store(true)
	s.sendWindow.priority.Store(true)
	s.receiveWindow.priority.Store(true)
}

func (s *Conn) Read(buf []byte) (n int, err error) {
	if s.IsClosed() || buf == nil {
		return 0, errors.New("the conn has closed")
	}
	if len(buf) == 0 {
		return 0, nil
	}
	// waiting for takeout from receive window finish or timeout
	n, err = s.receiveWindow.Read(buf, s.connId)
	return
}

func (s *Conn) Write(buf []byte) (n int, err error) {
	if s.IsClosed() {
		return 0, errors.New("the conn has closed")
	}
	if atomic.LoadUint32(&s.closingFlag) == 1 {
		return 0, errors.New("io: write on closed conn")
	}
	if len(buf) == 0 {
		return 0, nil
	}
	n, err = s.sendWindow.WriteFull(buf, s.connId)
	return
}

func (s *Conn) IsClosed() bool {
	return atomic.LoadUint32(&s.isClose) == 1
}

func (s *Conn) SetClosingFlag() {
	atomic.StoreUint32(&s.closingFlag, 1)
}

func (s *Conn) Close() (err error) {
	s.once.Do(func() {
		s.closeProcess(true)
	})
	return
}

func (s *Conn) closeLocal() {
	s.once.Do(func() {
		s.closeProcess(false)
	})
}

func (s *Conn) closeProcess(notifyRemote bool) {
	atomic.StoreUint32(&s.isClose, 1)
	s.receiveWindow.mux.connMap.Delete(s.connId)
	if notifyRemote && !s.receiveWindow.mux.IsClosed() {
		// if server or user close the conn while reading, will Get an io.EOF
		// and this Close method will be invoked, send this signal to close other side
		s.receiveWindow.mux.sendInfo(muxConnClose, s.connId, s.priority.Load(), nil)
	}
	s.sendWindow.CloseWindow()
	s.receiveWindow.CloseWindow()
	//return
}

func (s *Conn) LocalAddr() net.Addr {
	return s.receiveWindow.mux.conn.LocalAddr()
}

func (s *Conn) RemoteAddr() net.Addr {
	return s.receiveWindow.mux.conn.RemoteAddr()
}

func (s *Conn) SetDeadline(t time.Time) error {
	_ = s.SetReadDeadline(t)
	_ = s.SetWriteDeadline(t)
	return nil
}

func (s *Conn) SetReadDeadline(t time.Time) error {
	s.receiveWindow.SetTimeOut(t)
	return nil
}

func (s *Conn) SetWriteDeadline(t time.Time) error {
	s.sendWindow.SetTimeOut(t)
	return nil
}

type window struct {
	maxSizeDone uint64
	// 64bit alignment
	// maxSizeDone contains 4 parts
	//   1       31       1      31
	// wait   maxSize  useless  done
	// wait zero means false, one means true
	off       uint32
	closeOp   uint32        // set once by CloseWindow
	closeOpCh chan struct{} // closed by CloseWindow, broadcast to all waiters
	mux       *Mux
}

const windowBits = 31
const waitBits = dequeueBits + windowBits
const mask1 = 1
const mask31 = 1<<windowBits - 1

func (Self *window) unpack(ptrs uint64) (maxSize, done uint32, wait bool) {
	maxSize = uint32((ptrs >> dequeueBits) & mask31)
	done = uint32(ptrs & mask31)
	if ((ptrs >> waitBits) & mask1) == 1 {
		wait = true
		return
	}
	return
}

func (Self *window) pack(maxSize, done uint32, wait bool) uint64 {
	if wait {
		return (uint64(1)<<waitBits |
			uint64(maxSize&mask31)<<dequeueBits) |
			uint64(done&mask31)
	}
	return (uint64(0)<<waitBits |
		uint64(maxSize&mask31)<<dequeueBits) |
		uint64(done&mask31)
}

func (Self *window) New() {
	Self.closeOpCh = make(chan struct{})
}

// CloseWindow marks the window closed; only the first call closes closeOpCh.
func (Self *window) CloseWindow() {
	if atomic.CompareAndSwapUint32(&Self.closeOp, 0, 1) {
		close(Self.closeOpCh)
	}
}

func (Self *window) closed() bool {
	return atomic.LoadUint32(&Self.closeOp) == 1
}

type receiveWindow struct {
	window
	// readMu serializes readers and guards element, off and bw; release
	// takes it too, after Stop has woken any blocked reader.
	readMu   sync.Mutex
	bufQueue *receiveWindowQueue
	element  *listElement
	count    int8
	bw       *writeBandwidth
	priority atomic.Bool
	// receive window send the current max size and read size to send window
	// means done size actually store the size receive window has read
}

func (Self *receiveWindow) New(mux *Mux) {
	// initial a window for receive
	Self.bufQueue = newReceiveWindowQueue()
	Self.element = listEle.Get()
	Self.maxSizeDone = Self.pack(maximumSegmentSize*30, 0, false)
	Self.mux = mux
	Self.window.New()
	Self.bw = newWriteBandwidth()
}

func (Self *receiveWindow) remainingSize(maxSize uint32, delta uint16) (n uint32) {
	// receive window remaining
	l := int64(maxSize) - int64(Self.bufQueue.Len())
	l -= int64(delta)
	if l > 0 {
		n = uint32(l)
	}
	return
}

func (Self *receiveWindow) calcSize() {
	// calculating maximum receive window size
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
		if n < maximumSegmentSize*30 {
			n = maximumSegmentSize * 30
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
				if size == maximumSegmentSize*30 {
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
					logs.Println("window too large, calculated:", n, "limit:", limit, connBw, muxBw)
					n = limit
				}
			}
			// set the maximum size
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
	if Self.closed() {
		return errors.New("conn.receiveWindow: write on closed window")
	}
	element, err := newListElement(buf, l, part)
	if err != nil {
		return
	}
	Self.calcSize() // calculate the max window size
	var wait bool
	var maxSize, read uint32
start:
	ptrs := atomic.LoadUint64(&Self.maxSizeDone)
	maxSize, read, wait = Self.unpack(ptrs)
	remain := Self.remainingSize(maxSize, l)
	// calculate the remaining window size now, plus the element we will push
	if remain == 0 && !wait {
		wait = true
		if !atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, read, wait)) {
			// only change the wait status, not send the read size
			goto start
			// another goroutine change the status, make sure shall we need wait
		}
	} else if !wait {
		if !atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, 0, wait)) {
			// reset read size here, and send the read size directly
			goto start
			// another goroutine change the status, make sure shall we need wait
		}
	} // maybe there are still some data received even if window is full, just keep the wait status
	// and push into queue. when receive window read enough, send window will be acknowledged.
	Self.bufQueue.Push(element)
	// status check finish, now we can push the element into the queue
	if !wait {
		Self.mux.sendInfo(muxMsgSendOk, id, Self.priority.Load(), Self.pack(maxSize, read, false))
		// send the current status to send window
	}
	return nil
}

func (Self *receiveWindow) Read(p []byte, id int32) (n int, err error) {
	Self.readMu.Lock()
	defer Self.readMu.Unlock()
	if Self.closed() {
		return 0, io.EOF // receive close signal, returns eof
	}
	if Self.bufQueue.deadlineExceeded() {
		return 0, os.ErrDeadlineExceeded
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
		// on the first Read method invoked, Self.off and Self.element.l
		// both zero value
		listEle.Put(Self.element)
		Self.element = nil
		Self.off = 0
		var element *listElement
		if Self.closed() {
			err = io.EOF
		} else {
			// if the queue is empty, Pop waits until an element is pushed,
			// the queue is stopped, or the read deadline passes
			element, err = Self.bufQueue.Pop()
		}
		if err != nil {
			// keep an empty element so later reads stay well defined; a
			// deadline error leaves the stream and its queued data intact
			Self.element = listEle.Get()
			if n > 0 {
				// deliver what has been copied, the error repeats on the
				// next call
				err = nil
			}
			return
		}
		Self.element = element
	}
	l = copy(p[pOff:], Self.element.Buf[Self.off:Self.element.L])
	pOff += l
	Self.off += uint32(l)
	n += l
	//l = 0
	if Self.off == uint32(Self.element.L) {
		windowBuff.Put(Self.element.Buf)
		Self.sendStatus(id, Self.element.L)
		// check the window full status
	}
	if pOff < len(p) && Self.element.Part {
		// element is a part of the segments, trying to fill up buf p
		goto copyData
	}
	return // buf p is full or all segments in buf, return
}

func (Self *receiveWindow) sendStatus(id int32, l uint16) {
	var maxSize, read uint32
	var wait bool
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxSize, read, wait = Self.unpack(ptrs)
		if read <= (read+uint32(l))&mask31 {
			read += uint32(l)
			remain := Self.remainingSize(maxSize, 0)
			if wait && remain > 0 || read >= maxSize/2 || remain == maxSize {
				if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, 0, false)) {
					// now we get the current window status success
					// receive window free up some space we need acknowledge send window, also reset the read size
					// still having a condition that receive window is empty and not send the status to send window
					// so send the status here
					Self.mux.sendInfo(muxMsgSendOk, id, Self.priority.Load(), Self.pack(maxSize, read, false))
					break
				}
			} else {
				if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, read, wait)) {
					// receive window not into the wait status, or still not having any space now,
					// just change the read size
					break
				}
			}
		} else {
			//overflow
			if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, uint32(l), wait)) {
				// reset to l
				Self.mux.sendInfo(muxMsgSendOk, id, Self.priority.Load(), Self.pack(maxSize, read, false))
				break
			}
		}
		runtime.Gosched()
		// another goroutine change remaining or wait status, make sure
	}
	//return
}

func (Self *receiveWindow) SetTimeOut(t time.Time) {
	// waiting for FIFO queue Pop method
	Self.bufQueue.SetTimeOut(t)
}

func (Self *receiveWindow) Stop() {
	// queue has no more data to push, so unblock pop method; idempotent
	Self.bufQueue.Stop()
}

func (Self *receiveWindow) CloseWindow() {
	Self.window.CloseWindow()
	// Stop wakes a reader blocked in Pop, so the readMu taken by release
	// is always released promptly
	Self.Stop()
	Self.release()
}

func (Self *receiveWindow) release() {
	Self.readMu.Lock()
	defer Self.readMu.Unlock()
	if Self.element != nil {
		if Self.off < uint32(Self.element.L) && Self.element.Buf != nil {
			// a fully read element has already returned its buffer
			windowBuff.Put(Self.element.Buf)
		}
		listEle.Put(Self.element)
		Self.element = listEle.Get()
		Self.off = 0
	}
	for {
		ele := Self.bufQueue.TryPop()
		if ele == nil {
			return
		}
		if ele.Buf != nil {
			windowBuff.Put(ele.Buf)
		}
		listEle.Put(ele)
	} // release resource
}

type sendWindow struct {
	window
	// deadline is the write deadline in UnixNano, zero means no deadline.
	deadline atomic.Int64
	// writeMu serializes writers and guards buf and off.
	writeMu    sync.Mutex
	buf        []byte
	setSizeCh  chan struct{} // capacity 1, window grew while the writer waits
	deadlineCh chan struct{} // capacity 1, deadline changed
	priority   atomic.Bool
	// send window receive the receiving window max size and read size
	// done size store the size send window has sent, send and read will be totally equal
	// so send minus read, send window can get the current window size remaining
}

func (Self *sendWindow) New(mux *Mux) {
	Self.setSizeCh = make(chan struct{}, 1)
	Self.deadlineCh = make(chan struct{}, 1)
	Self.maxSizeDone = Self.pack(maximumSegmentSize*30, 0, false)
	Self.mux = mux
	Self.window.New()
}

func (Self *sendWindow) SetSendBuf(buf []byte) {
	// send window buff from conn write method, set it to send window
	Self.buf = buf
	Self.off = 0
}

func (Self *sendWindow) remainingSize(maxSize, send uint32) uint32 {
	l := int64(maxSize&mask31) - int64(send&mask31)
	if l > 0 {
		return uint32(l)
	}
	return 0
}

func (Self *sendWindow) SetSize(currentMaxSizeDone uint64) (closed bool) {
	// set the window size from receive window
	if Self.closed() {
		// a blocked writer is woken by closeOpCh
		return true
	}
	var maxsize, send uint32
	var wait, newWait bool
	currentMaxSize, read, _ := Self.unpack(currentMaxSizeDone)
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxsize, send, wait = Self.unpack(ptrs)
		if read > send {
			logs.Println("window read > send: max size:", currentMaxSize, "read:", read, "send", send)
			return
		}
		if read == 0 && currentMaxSize == maxsize {
			return
		}
		outstanding := (send - read) & mask31
		remain := Self.remainingSize(currentMaxSize, outstanding)
		nextWait := remain == 0 && wait

		// remain > 0, change wait to false. or remain == 0, wait is false, just keep it
		if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(currentMaxSize, outstanding, nextWait)) {
			//logs.Printf("currentMaxSize:%d read:%d send(outstanding):%d", currentMaxSize, read, outstanding)
			newWait = nextWait
			break
		}
		// another goroutine change wait status or window size
	}
	if wait && !newWait {
		// send window into the wait status, need notice the channel
		Self.allow()
	}
	// send window not into the wait status, so just do slide
	return false
}

func (Self *sendWindow) allow() {
	// setSizeCh is never closed; a stale token only causes the writer to
	// re-check the window
	select {
	case Self.setSizeCh <- struct{}{}:
	default:
	}
}

func (Self *sendWindow) sent(sentSize uint32) {
	var maxSie, send uint32
	var wait bool
	for {
		ptrs := atomic.LoadUint64(&Self.maxSizeDone)
		maxSie, send, wait = Self.unpack(ptrs)
		if (send+sentSize)&mask31 < send {
			// overflow
			runtime.Gosched()
			continue
		}
		if atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSie, send+sentSize, wait)) {
			// set the send size
			break
		}
	}
}

func (Self *sendWindow) WriteTo() (p []byte, sendSize uint32, part bool, err error) {
	// returns buf segments, return only one segments, need a loop outside
	// until err = io.EOF
	if Self.closed() {
		return nil, 0, false, errWriteWindowClosed
	}
	if Self.off >= uint32(len(Self.buf)) {
		return nil, 0, false, io.EOF
		// send window buff is drain, return eof and get another one
	}
	var maxSize, send uint32
start:
	ptrs := atomic.LoadUint64(&Self.maxSizeDone)
	maxSize, send, _ = Self.unpack(ptrs)
	remain := Self.remainingSize(maxSize, send)
	if remain == 0 {
		if !atomic.CompareAndSwapUint64(&Self.maxSizeDone, ptrs, Self.pack(maxSize, send, true)) {
			// just change the status wait status
			goto start // another goroutine change the window, try again
		}
		// into the wait status
		err = Self.waitReceiveWindow()
		if err != nil {
			return nil, 0, false, err
		}
		goto start
	}

	if Self.off > uint32(len(Self.buf)) {
		return nil, 0, false, io.EOF
	}

	//logs.Info("Self  size:", len(Self.buf), "Self.off " ,Self.off)
	// there are still remaining window
	if len(Self.buf[Self.off:]) > maximumSegmentSize {
		sendSize = maximumSegmentSize
	} else {
		sendSize = uint32(len(Self.buf[Self.off:]))
	}
	if remain < sendSize {
		// usable window size is smaller than
		// window MAXIMUM_SEGMENT_SIZE or send buf left
		sendSize = remain
	}
	if sendSize < uint32(len(Self.buf[Self.off:])) {
		part = true
	}
	p = Self.buf[Self.off : sendSize+Self.off]
	Self.off += sendSize
	Self.sent(sendSize)
	//logs.Info("sendSize", sendSize, "Self.off " ,Self.off)
	return
}

type windowClosedError struct{}

func (windowClosedError) Error() string { return "conn.writeWindow: window closed" }
func (windowClosedError) Unwrap() error { return net.ErrClosed }

var errWriteWindowClosed error = windowClosedError{}

func (Self *sendWindow) waitReceiveWindow() (err error) {
	// waiting for receive usable window size, close, or deadline
	drainSignal(Self.deadlineCh)
	for {
		timer, timeout, expired := deadlineTimer(Self.deadline.Load())
		if expired {
			return os.ErrDeadlineExceeded
		}
		select {
		case <-Self.setSizeCh:
			stopTimer(timer)
			return nil
		case <-Self.closeOpCh:
			stopTimer(timer)
			return errWriteWindowClosed
		case <-Self.deadlineCh:
			stopTimer(timer)
			// deadline changed, recompute
		case <-timeout:
			return os.ErrDeadlineExceeded
		}
	}
}

func (Self *sendWindow) WriteFull(buf []byte, id int32) (n int, err error) {
	Self.writeMu.Lock()
	defer Self.writeMu.Unlock()
	if deadlinePassed(Self.deadline.Load()) {
		return 0, os.ErrDeadlineExceeded
	}
	Self.SetSendBuf(buf) // set the buf to send window
	defer Self.SetSendBuf(nil)
	var bufSeg []byte
	var part bool
	var l uint32
	for {
		if Self.off < uint32(len(Self.buf)) {
			// wait for queue room before taking a segment, so a failed wait
			// never consumes send window for data that was not sent
			if err = Self.mux.applyWriteBackpressure(Self); err != nil {
				break
			}
		}
		bufSeg, l, part, err = Self.WriteTo()
		// get the buf segments from send window
		if bufSeg == nil && !part && err == io.EOF {
			// send window is drain, break the loop
			err = nil
			break
		}
		if err != nil {
			break
		}
		flag := muxNewMsg
		if part {
			flag = muxNewMsgPart
		}
		// send to other side, not send nil data to other side
		if !Self.mux.sendInfo(flag, id, Self.priority.Load(), bufSeg) {
			err = fmt.Errorf("conn.writeWindow: mux closed: %w", net.ErrClosed)
			break
		}
		n += int(l)
	}
	return
}

func (Self *sendWindow) SetTimeOut(t time.Time) {
	Self.deadline.Store(deadlineNano(t))
	// wake a writer blocked on the window so it re-evaluates the deadline
	notifyDeadline(Self.deadlineCh)
}

type writeBandwidth struct {
	writeBW   uint64 // store in bits, but it's float64
	readEnd   time.Time
	duration  float64
	bufLength uint32
	ratio     uint32
}

const writeCalcThreshold uint32 = 5 * 1024 * 1024

func newWriteBandwidth() *writeBandwidth {
	return &writeBandwidth{ratio: 1}
}

func (Self *writeBandwidth) StartRead() {
	if Self.readEnd.IsZero() {
		Self.readEnd = time.Now()
	}
	Self.duration += time.Since(Self.readEnd).Seconds()
	if Self.bufLength >= writeCalcThreshold*atomic.LoadUint32(&Self.ratio) {
		Self.calcBandWidth()
	}
}

func (Self *writeBandwidth) SetCopySize(n uint16) {
	Self.bufLength += uint32(n)
	Self.endRead()
}

func (Self *writeBandwidth) endRead() {
	Self.readEnd = time.Now()
}

func (Self *writeBandwidth) calcBandWidth() {
	atomic.StoreUint64(&Self.writeBW, math.Float64bits(float64(Self.bufLength)/Self.duration))
	Self.bufLength = 0
	Self.duration = 0
}

func (Self *writeBandwidth) Get() (bw float64) {
	// The zero value, 0 for numeric types
	bw = math.Float64frombits(atomic.LoadUint64(&Self.writeBW))
	if bw <= 0 {
		bw = 0
	}
	return
}

func (Self *writeBandwidth) GrowRatio() {
	atomic.AddUint32(&Self.ratio, 1)
}
