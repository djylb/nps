package mux

import (
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type priorityQueue struct {
	length       uint64
	highestChain *bufChain
	middleChain  *bufChain
	lowestChain  *bufChain
	starving     uint8
	stop         uint32
	cond         *sync.Cond // wakes the consumer; only Push/Pop/Stop use it
	// lowWaterCh is closed (and replaced lazily) when the length drops below
	// writeQueueLowWater or the queue stops. It is kept apart from cond so
	// the consumer, which pops while holding cond.L, never needs another
	// lock it could already hold.
	lowWaterMu sync.Mutex
	lowWaterCh chan struct{}
}

func (Self *priorityQueue) New() {
	Self.highestChain = new(bufChain)
	Self.highestChain.new(4)
	Self.middleChain = new(bufChain)
	Self.middleChain.new(32)
	Self.lowestChain = new(bufChain)
	Self.lowestChain.new(256)
	locker := new(sync.Mutex)
	Self.cond = sync.NewCond(locker)
}

func (Self *priorityQueue) Push(packager *muxPackager) {
	atomic.AddUint64(&Self.length, 1)
	Self.push(packager)

	Self.cond.L.Lock()
	//Self.cond.Broadcast()
	Self.cond.Signal()
	Self.cond.L.Unlock()
	//return
}

func (Self *priorityQueue) push(packager *muxPackager) {
	switch packager.flag {
	case muxPingFlag, muxPingReturn:
		Self.highestChain.pushHead(unsafe.Pointer(packager))
		// ping packages need highest priority
	case muxNewConn, muxNewConnOk, muxNewConnFail:
		// new conn packages need some priority too
		Self.middleChain.pushHead(unsafe.Pointer(packager))
	case muxMsgSendOk:
		// window updates describe the receive window of the opposite
		// direction; sending them ahead of queued data frames is safe and
		// keeps the peer's send window from stalling behind our own data
		Self.middleChain.pushHead(unsafe.Pointer(packager))
	default:
		if packager.priority {
			Self.middleChain.pushHead(unsafe.Pointer(packager))
		} else {
			Self.lowestChain.pushHead(unsafe.Pointer(packager))
		}
	}
}

func (Self *priorityQueue) Len() uint64 {
	return atomic.LoadUint64(&Self.length)
}

func (Self *priorityQueue) afterPop() {
	// atomic length--
	newLen := atomic.AddUint64(&Self.length, ^uint64(0))

	// Wake up backpressure waiters only when crossing below low water:
	// oldLen >= lowWater && newLen < lowWater
	// (oldLen is newLen+1 because we just decremented)
	if newLen < writeQueueLowWater {
		oldLen := newLen + 1
		if oldLen >= writeQueueLowWater {
			Self.wakeLowWater()
		}
	}
}

// lowWater returns the channel closed by the next low water crossing.
// Callers re-check the length after fetching it, so no crossing is missed.
func (Self *priorityQueue) lowWater() <-chan struct{} {
	Self.lowWaterMu.Lock()
	defer Self.lowWaterMu.Unlock()
	if Self.lowWaterCh == nil {
		Self.lowWaterCh = make(chan struct{})
	}
	return Self.lowWaterCh
}

func (Self *priorityQueue) wakeLowWater() {
	Self.lowWaterMu.Lock()
	if Self.lowWaterCh != nil {
		close(Self.lowWaterCh)
		Self.lowWaterCh = nil
	}
	Self.lowWaterMu.Unlock()
}

func (Self *priorityQueue) stopped() bool {
	return atomic.LoadUint32(&Self.stop) == 1
}

const maxStarving uint8 = 8

func (Self *priorityQueue) Pop() (packager *muxPackager) {
	var iter bool
	for {
		packager = Self.TryPop()
		if packager != nil {
			return
		}
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		if iter {
			break
			// trying to pop twice
		}
		iter = true
		runtime.Gosched()
	}
	Self.cond.L.Lock()
	defer Self.cond.L.Unlock()
	for packager = Self.TryPop(); packager == nil; {
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		Self.cond.Wait()
		// wait for it with no more iter
		packager = Self.TryPop()
	}
	return
}

func (Self *priorityQueue) TryPop() (packager *muxPackager) {
	ptr, ok := Self.highestChain.popTail()
	if ok {
		packager = (*muxPackager)(ptr)
		Self.afterPop()
		return
	}
	if Self.starving < maxStarving {
		// not pop too much, lowestChain will wait too long
		ptr, ok = Self.middleChain.popTail()
		if ok {
			packager = (*muxPackager)(ptr)
			Self.starving++
			Self.afterPop()
			return
		}
	}
	ptr, ok = Self.lowestChain.popTail()
	if ok {
		packager = (*muxPackager)(ptr)
		if Self.starving > 0 {
			Self.starving = Self.starving / 2
		}
		Self.afterPop()
		return
	}
	if Self.starving > 0 {
		ptr, ok = Self.middleChain.popTail()
		if ok {
			packager = (*muxPackager)(ptr)
			Self.starving++
			Self.afterPop()
			return
		}
	}
	return
}

// drainPop pops any queued packager without touching the starving counter,
// so it may run concurrently with the single TryPop/Pop consumer.
func (Self *priorityQueue) drainPop() *muxPackager {
	for _, chain := range []*bufChain{Self.highestChain, Self.middleChain, Self.lowestChain} {
		if ptr, ok := chain.popTail(); ok {
			Self.afterPop()
			return (*muxPackager)(ptr)
		}
	}
	return nil
}

func (Self *priorityQueue) Stop() {
	atomic.StoreUint32(&Self.stop, 1)
	Self.cond.L.Lock()
	Self.cond.Broadcast()
	Self.cond.L.Unlock()
	Self.wakeLowWater()
}

type connQueue struct {
	chain *bufChain
	//starving uint8
	stop uint32
	cond *sync.Cond
}

func (Self *connQueue) New() {
	Self.chain = new(bufChain)
	Self.chain.new(32)
	locker := new(sync.Mutex)
	Self.cond = sync.NewCond(locker)
}

func (Self *connQueue) Push(connection *Conn) {
	Self.chain.pushHead(unsafe.Pointer(connection))
	// broadcast while holding the lock, so a consumer that has checked the
	// queue but not yet entered Wait cannot miss the wake-up
	Self.cond.L.Lock()
	Self.cond.Broadcast()
	Self.cond.L.Unlock()
}

func (Self *connQueue) Pop() (connection *Conn) {
	var iter bool
	for {
		connection = Self.TryPop()
		if connection != nil {
			return
		}
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		if iter {
			break
			// trying to pop twice
		}
		iter = true
		runtime.Gosched()
	}
	Self.cond.L.Lock()
	defer Self.cond.L.Unlock()
	for connection = Self.TryPop(); connection == nil; {
		if atomic.LoadUint32(&Self.stop) == 1 {
			return
		}
		Self.cond.Wait()
		// wait for it with no more iter
		connection = Self.TryPop()
	}
	return
}

func (Self *connQueue) TryPop() (connection *Conn) {
	ptr, ok := Self.chain.popTail()
	if ok {
		connection = (*Conn)(ptr)
		return
	}
	return
}

func (Self *connQueue) Stop() {
	atomic.StoreUint32(&Self.stop, 1)
	Self.cond.L.Lock()
	Self.cond.Broadcast()
	Self.cond.L.Unlock()
}

type listElement struct {
	Buf  []byte
	L    uint16
	Part bool
}

func (Self *listElement) Reset() {
	Self.L = 0
	Self.Buf = nil
	Self.Part = false
}

func newListElement(buf []byte, l uint16, part bool) (element *listElement, err error) {
	if uint16(len(buf)) < l {
		err = errors.New("listElement: buf length not match")
		return
	}
	element = listEle.Get()
	element.Buf = buf
	element.L = l
	element.Part = part
	return
}

type receiveWindowQueue struct {
	// lengthWait packs the queued byte length (high 32 bits) and the reader
	// wait flag (low 32 bits); it must stay the first word for 64-bit
	// alignment on 32-bit platforms.
	lengthWait uint64
	// deadline is the read deadline in UnixNano, zero means no deadline.
	deadline   atomic.Int64
	chain      *bufChain
	stopOp     chan struct{} // closed once, no more data will be pushed
	stopOnce   sync.Once
	readOp     chan struct{} // capacity 1, data pushed while the reader waits
	deadlineCh chan struct{} // capacity 1, deadline changed
}

func newReceiveWindowQueue() *receiveWindowQueue {
	queue := receiveWindowQueue{
		chain:      new(bufChain),
		stopOp:     make(chan struct{}),
		readOp:     make(chan struct{}, 1),
		deadlineCh: make(chan struct{}, 1),
	}
	queue.chain.new(64)
	return &queue
}

func (Self *receiveWindowQueue) Push(element *listElement) {
	var length, wait uint32
	for {
		ptrs := atomic.LoadUint64(&Self.lengthWait)
		length, wait = dequeueUnpack(ptrs)
		length += uint32(element.L)
		if atomic.CompareAndSwapUint64(&Self.lengthWait, ptrs, dequeuePack(length, 0)) {
			break
		}
		// another goroutine change the length or into wait, make sure
	}
	Self.chain.pushHead(unsafe.Pointer(element))
	if wait == 1 {
		Self.allowPop()
	}
}

func (Self *receiveWindowQueue) Pop() (element *listElement, err error) {
	for {
		ptrs := atomic.LoadUint64(&Self.lengthWait)
		length, _ := dequeueUnpack(ptrs)
		if length > 0 {
			element = Self.TryPop()
			if element != nil {
				return element, nil
			}
			// a push has published the length but not the element yet, or a
			// concurrent release drained it; reload the length and retry
			runtime.Gosched()
			continue
		}
		if Self.stopped() {
			// nothing left and no more data will arrive
			return nil, io.EOF
		}
		if !atomic.CompareAndSwapUint64(&Self.lengthWait, ptrs, dequeuePack(0, 1)) {
			continue // another goroutine is pushing
		}
		if err = Self.waitPush(); err != nil && err != io.EOF {
			return nil, err
		}
		// woken by a push or a stop: re-check, data queued before the stop
		// is still delivered
	}
}

func (Self *receiveWindowQueue) TryPop() (element *listElement) {
	ptr, ok := Self.chain.popTail()
	if ok {
		element = (*listElement)(ptr)
		atomic.AddUint64(&Self.lengthWait, ^(uint64(element.L)<<dequeueBits - 1))
		return
	}
	return nil
}

// allowPop wakes a waiting reader. It never blocks: a token left over from
// an earlier wake-up (for example after a read deadline) is harmless because
// Pop re-checks the length after every wake-up.
func (Self *receiveWindowQueue) allowPop() {
	select {
	case Self.readOp <- struct{}{}:
	default:
	}
}

func (Self *receiveWindowQueue) stopped() bool {
	select {
	case <-Self.stopOp:
		return true
	default:
		return false
	}
}

func (Self *receiveWindowQueue) waitPush() error {
	// discard a stale deadline notification before reading the deadline,
	// the value loaded below already reflects it
	drainSignal(Self.deadlineCh)
	for {
		timer, timeout, expired := deadlineTimer(Self.deadline.Load())
		if expired {
			return os.ErrDeadlineExceeded
		}
		select {
		case <-Self.readOp:
			stopTimer(timer)
			return nil
		case <-Self.stopOp:
			stopTimer(timer)
			return io.EOF
		case <-Self.deadlineCh:
			stopTimer(timer)
			// deadline changed, recompute
		case <-timeout:
			return os.ErrDeadlineExceeded
		}
	}
}

func (Self *receiveWindowQueue) Len() (n uint32) {
	ptrs := atomic.LoadUint64(&Self.lengthWait)
	n, _ = dequeueUnpack(ptrs)
	return
}

func (Self *receiveWindowQueue) Stop() {
	Self.stopOnce.Do(func() {
		close(Self.stopOp)
	})
}

func (Self *receiveWindowQueue) SetTimeOut(t time.Time) {
	Self.deadline.Store(deadlineNano(t))
	notifyDeadline(Self.deadlineCh)
}

// deadlineExceeded reports whether the deadline is set and has passed.
func (Self *receiveWindowQueue) deadlineExceeded() bool {
	return deadlinePassed(Self.deadline.Load())
}

var (
	minDeadline = time.Unix(0, math.MinInt64)
	maxDeadline = time.Unix(0, math.MaxInt64)
)

// deadlineNano converts a deadline to its stored form, zero means none.
// Times outside the UnixNano range are clamped: earlier ones are already
// past, later ones are effectively never reached.
func deadlineNano(t time.Time) int64 {
	switch {
	case t.IsZero():
		return 0
	case t.Before(minDeadline):
		return 1
	case t.After(maxDeadline):
		return math.MaxInt64
	}
	n := t.UnixNano()
	if n == 0 {
		n = 1 // keep a real deadline distinguishable from "none"
	}
	return n
}

func deadlinePassed(deadline int64) bool {
	return deadline != 0 && time.Now().UnixNano() >= deadline
}

// deadlineTimer returns a timer channel for the stored deadline; the channel
// is nil when no deadline is set, expired is true when it has already passed.
func deadlineTimer(deadline int64) (timer *time.Timer, c <-chan time.Time, expired bool) {
	if deadline == 0 {
		return nil, nil, false
	}
	d := time.Until(time.Unix(0, deadline))
	if d <= 0 {
		return nil, nil, true
	}
	timer = time.NewTimer(d)
	return timer, timer.C, false
}

func stopTimer(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
}

func drainSignal(ch chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

func notifyDeadline(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// https://golang.org/src/sync/poolqueue.go

type bufDequeue struct {
	// headTail packs together a 32-bit head index and a 32-bit
	// tail index. Both are indexes into vals modulo len(vals)-1.
	//
	// tail = index of oldest data in queue
	// head = index of next slot to fill
	//
	// Slots in the range [tail, head) are owned by consumers.
	// A consumer continues to own a slot outside this range until
	// it nils the slot, at which point ownership passes to the
	// producer.
	//
	// The head index is stored in the most-significant bits so
	// that we can atomically add to it and the overflow is
	// harmless.
	headTail uint64

	// vals is a ring buffer of interface{} values stored in this
	// dequeue. The size of this must be a power of 2.
	//
	// A slot is still in use until *both* the tail
	// index has moved beyond it and typ has been Set to nil. This
	// is Set to nil atomically by the consumer and read
	// atomically by the producer.
	vals     []unsafe.Pointer
	starving uint32
}

const dequeueBits = 32

// dequeueLimit is the maximum size of a bufDequeue.
//
// This must be at most (1<<dequeueBits)/2 because detecting fullness
// depends on wrapping around the ring buffer without wrapping around
// the index. We divide by 4 so this fits in an int on 32-bit.
const dequeueLimit = (1 << dequeueBits) / 4

func dequeueUnpack(ptrs uint64) (head, tail uint32) {
	const mask = 1<<dequeueBits - 1
	head = uint32((ptrs >> dequeueBits) & mask)
	tail = uint32(ptrs & mask)
	return
}

func dequeuePack(head, tail uint32) uint64 {
	const mask = 1<<dequeueBits - 1
	return (uint64(head) << dequeueBits) |
		uint64(tail&mask)
}

func (d *bufDequeue) unpack(ptrs uint64) (head, tail uint32) {
	return dequeueUnpack(ptrs)
}

func (d *bufDequeue) pack(head, tail uint32) uint64 {
	return dequeuePack(head, tail)
}

// pushHead adds val at the head of the queue. It returns false if the
// queue is full.
func (d *bufDequeue) pushHead(val unsafe.Pointer) bool {
	var slot *unsafe.Pointer
	var starve uint8
	if atomic.LoadUint32(&d.starving) > 0 {
		runtime.Gosched()
	}
	for {
		ptrs := atomic.LoadUint64(&d.headTail)
		head, tail := d.unpack(ptrs)
		if (tail+uint32(len(d.vals)))&(1<<dequeueBits-1) == head {
			// Queue is full.
			return false
		}
		ptrs2 := d.pack(head+1, tail)
		if atomic.CompareAndSwapUint64(&d.headTail, ptrs, ptrs2) {
			slot = &d.vals[head&uint32(len(d.vals)-1)]
			if starve >= 3 && atomic.LoadUint32(&d.starving) > 0 {
				atomic.StoreUint32(&d.starving, 0)
			}
			break
		}
		starve++
		if starve >= 3 {
			atomic.StoreUint32(&d.starving, 1)
		}
	}
	// The head slot is free, so we own it. Publish with a release store so
	// consumers loading the slot atomically observe a fully built value.
	atomic.StorePointer(slot, val)
	return true
}

// popTail removes and returns the element at the tail of the queue.
// It returns false if the queue is empty. It may be called by any
// number of consumers.
func (d *bufDequeue) popTail() (unsafe.Pointer, bool) {
	var val unsafe.Pointer
	var head, tail uint32
	for {
		ptrs := atomic.LoadUint64(&d.headTail)
		head, tail = d.unpack(ptrs)
		if tail == head {
			// Queue is empty.
			return nil, false
		}
		slot := &d.vals[tail&uint32(len(d.vals)-1)]
		val = atomic.LoadPointer(slot)
		if val != nil {
			// We now get a slot.
			if atomic.CompareAndSwapPointer(slot, val, nil) {
				break
				// Tell pushHead that we're done with this slot. Zeroing the
				// slot is also important so we don't leave behind references
				// that could keep this object live longer than necessary.
				//
				// We write to val first and then publish that we're done with
			}
		}
		// Maybe the value was taken by other goroutine or not push yet.
	}
	// At this point pushHead owns the slot.
	if tail < math.MaxUint32 {
		atomic.AddUint64(&d.headTail, 1)
	} else {
		atomic.AddUint64(&d.headTail, ^uint64(math.MaxUint32-1))
	}
	return val, true
}

// bufChain is a dynamically-sized version of bufDequeue.
//
// This is implemented as a doubly-linked list queue of poolDequeues
// where each dequeue is double the size of the previous one. Once a
// dequeue fills up, this allocates a New one and only ever pushes to
// the latest dequeue. Pops happen from the other end of the list and
// once a dequeue is exhausted, it gets removed from the list.
type bufChain struct {
	// head is the bufDequeue to push to. This is only accessed
	// by the producer, so doesn't need to be synchronized.
	head *bufChainElt

	// tail is the bufDequeue to popTail from. This is accessed
	// by consumers, so reads and writes must be atomic.
	tail     *bufChainElt
	newChain uint32
}

type bufChainElt struct {
	bufDequeue

	// next and prev link to the adjacent poolChainElts in this
	// bufChain.
	//
	// next is written atomically by the producer and read
	// atomically by the consumer. It only transitions from nil to
	// non-nil.
	//
	// prev is written atomically by the consumer and read
	// atomically by the producer. It only transitions from
	// non-nil to nil.
	next, prev *bufChainElt
}

func storePoolChainElt(pp **bufChainElt, v *bufChainElt) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(pp)), unsafe.Pointer(v))
}

func loadPoolChainElt(pp **bufChainElt) *bufChainElt {
	return (*bufChainElt)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(pp))))
}

func (c *bufChain) new(initSize int) {
	// Initialize the chain.
	// initSize must be a power of 2
	d := new(bufChainElt)
	d.vals = make([]unsafe.Pointer, initSize)
	storePoolChainElt(&c.head, d)
	storePoolChainElt(&c.tail, d)
}

func (c *bufChain) pushHead(val unsafe.Pointer) {
startPush:
	for {
		if atomic.LoadUint32(&c.newChain) > 0 {
			runtime.Gosched()
		} else {
			break
		}
	}

	d := loadPoolChainElt(&c.head)

	if d.pushHead(val) {
		return
	}

	// The current dequeue is full. Allocate a New one of twice
	// the size.
	if atomic.CompareAndSwapUint32(&c.newChain, 0, 1) {
		newSize := len(d.vals) * 2
		if newSize >= dequeueLimit {
			// Can't make it any bigger.
			newSize = dequeueLimit
		}

		d2 := &bufChainElt{prev: d}
		d2.vals = make([]unsafe.Pointer, newSize)
		d2.pushHead(val)
		storePoolChainElt(&c.head, d2)
		storePoolChainElt(&d.next, d2)
		atomic.StoreUint32(&c.newChain, 0)
		return
	}
	goto startPush
}

func (c *bufChain) popTail() (unsafe.Pointer, bool) {
	d := loadPoolChainElt(&c.tail)
	if d == nil {
		return nil, false
	}

	for {
		// It's important that we load the next pointer
		// *before* popping the tail. In general, d may be
		// transiently empty, but if next is non-nil before
		// the TryPop and the TryPop fails, then d is permanently
		// empty, which is the only condition under which it's
		// safe to drop d from the chain.
		d2 := loadPoolChainElt(&d.next)

		if val, ok := d.popTail(); ok {
			return val, ok
		}

		if d2 == nil {
			// This is the only dequeue. It's empty right
			// now, but could be pushed to in the future.
			return nil, false
		}

		// The tail of the chain has been drained, so move on
		// to the next dequeue. Try to drop it from the chain
		// so the next TryPop doesn't have to look at the empty
		// dequeue again.
		if atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&c.tail)), unsafe.Pointer(d), unsafe.Pointer(d2)) {
			// We won the race. Clear the prev pointer so
			// the garbage collector can collect the empty
			// dequeue and so popHead doesn't back up
			// further than necessary.
			storePoolChainElt(&d2.prev, nil)
		}
		d = d2
	}
}
