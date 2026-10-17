package mux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	connx "github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

const (
	muxPingFlag uint8 = iota
	muxNewConnOk
	muxNewConnFail
	muxNewMsg
	muxNewMsgPart
	muxMsgSendOk
	muxNewConn
	muxConnClose
	muxPingReturn
	muxPing            int32 = -1
	maximumSegmentSize       = poolSizeWindow
	maximumWindowSize        = 1 << 27 // 1<<31-1 TCP slide window size is very large,
	// we use 128M, reduce memory usage
)

var (
	PingInterval = 5 * time.Second
	PingJitter   = 2 * time.Second
	PingMaxPad   = 16
)

const (
	writeQueueHighWater uint64 = 2048
	writeQueueLowWater  uint64 = 1024
)

type Mux struct {
	latency       uint64 // we store latency in bits, but it's float64
	lastAliveTime int64
	net.Listener
	conn         net.Conn
	connMap      *ConnMap
	newConnCh    chan *Conn
	id           int32
	isInitiator  bool
	closeChan    chan struct{}
	counter      *latencyCounter
	bw           *Bandwidth
	pingCh       chan *muxPackager
	pingTimeout  time.Duration
	connType     string
	writeQueue   priorityQueue
	newConnQueue connQueue
	once         sync.Once
	reasonMu     sync.RWMutex
	closeReason  string
}

func NewMux(c net.Conn, connType string, pingCheckThreshold int, isInitiator bool) *Mux {
	//c.(*net.TCPConn).SetReadBuffer(0)
	//c.(*net.TCPConn).SetWriteBuffer(0)
	fd, err := getConnFd(c)
	if err != nil {
		logs.Println(err)
	}
	var pingTimeout time.Duration
	if pingCheckThreshold > 0 {
		pingTimeout = time.Duration(pingCheckThreshold) * time.Second
	} else if connType == "kcp" {
		pingTimeout = PingInterval + 2*PingJitter
	} else {
		pingTimeout = 2 * (PingInterval + PingJitter)
	}
	if maxTimeout := 10 * (PingInterval + PingJitter); pingTimeout > maxTimeout {
		pingTimeout = maxTimeout
	}
	if pingTimeout <= PingInterval+PingJitter {
		pingTimeout = 30 * time.Second
	}
	var startId int32
	if isInitiator {
		startId = -1
	} else {
		startId = 0
	}
	m := &Mux{
		conn:          c,
		connMap:       NewConnMap(),
		id:            startId,
		closeChan:     make(chan struct{}, 1),
		newConnCh:     make(chan *Conn),
		bw:            NewBandwidth(fd),
		connType:      connType,
		pingCh:        make(chan *muxPackager),
		pingTimeout:   pingTimeout,
		lastAliveTime: time.Now().UnixNano(),
		counter:       newLatencyCounter(),
	}
	m.writeQueue.New()
	m.newConnQueue.New()
	//read session by flag
	m.readSession()
	//ping
	m.ping()
	m.writeSession()
	return m
}

func (s *Mux) markAlive() {
	atomic.StoreInt64(&s.lastAliveTime, time.Now().UnixNano())
}

func (s *Mux) isAliveTimeout(now time.Time) bool {
	last := atomic.LoadInt64(&s.lastAliveTime)
	if last <= 0 {
		return false
	}
	return now.Sub(time.Unix(0, last)) > s.pingTimeout
}

func normalizedPingJitter() time.Duration {
	return absoluteDuration(PingJitter)
}

func absoluteDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func nextPingDelay() time.Duration {
	if PingInterval <= 0 {
		return time.Second
	}
	jitter := normalizedPingJitter()
	if jitter == 0 {
		return PingInterval
	}
	j := time.Duration(rand.Int63n(int64(jitter))) - jitter/2
	next := PingInterval + j
	if next <= 0 {
		return PingInterval
	}
	return next
}

func (s *Mux) NewConn() (*Conn, error) {
	if s.IsClosed() {
		return nil, s.closedErr("the mux has closed")
	}
	conn := NewConn(s.getId(), s)
	// it must be Set before send
	s.connMap.Set(conn.connId, conn)
	s.sendInfo(muxNewConn, conn.connId, false, nil)
	//Set a timer timeout
	timeout := s.pingTimeout
	if s.connType == "kcp" {
		if timeout > 8*time.Second {
			timeout = 8 * time.Second
		}
	} else {
		if timeout > 15*time.Second {
			timeout = 15 * time.Second
		}
	}
	if timeout < 8*time.Second {
		timeout = 8 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-conn.connStatusOkCh:
		return conn, nil
	case <-conn.connStatusFailCh:
		conn.closeLocal()
		return nil, errors.New("create connection fail, the server refused the connection")

	case <-s.closeChan:
		conn.closeLocal()
		return nil, s.closedErr("create connection fail, the mux has closed")

	case <-timer.C:
		conn.closeLocal()
		return nil, errors.New("create connection timeout")
	}
}

func (s *Mux) Accept() (net.Conn, error) {
	select {
	case <-s.closeChan:
		return nil, s.closedErr("accept error: the mux has closed")
	case conn, ok := <-s.newConnCh:
		if !ok || conn == nil {
			return nil, errors.New("accept error: the connection has been closed")
		}
		if s.IsClosed() {
			// both cases were ready; never hand out a stream of a closed mux
			_ = conn.Close()
			return nil, s.closedErr("accept error: the mux has closed")
		}
		return conn, nil
	}
}

func (s *Mux) Addr() net.Addr {
	return s.conn.LocalAddr()
}

// sendInfo queues a frame and reports whether it was accepted; it returns
// false when the mux is closed.
func (s *Mux) sendInfo(flag uint8, id int32, priority bool, data interface{}) bool {
	if s.IsClosed() {
		return false
	}

	pack := muxPack.Get()
	pack.priority = priority
	if err := pack.Set(flag, id, data); err != nil {
		muxPack.Put(pack)
		logs.Println("mux: New Pack err", err)
		// close asynchronously: the caller may hold a stream lock that
		// closing the streams also needs
		go func() { _ = s.Close() }()
		return false
	}
	s.writeQueue.Push(pack)
	if s.IsClosed() {
		// closed concurrently, release may already have drained the queue;
		// recycle anything left behind
		s.drainWriteQueue()
		return false
	}
	return true
}

// applyWriteBackpressure blocks a data writer while the write queue is above
// the high water mark, until it drains below the low water mark. The wait
// also ends when the mux or the stream closes or the write deadline passes.
func (s *Mux) applyWriteBackpressure(w *sendWindow) error {
	q := &s.writeQueue
	if w.priority.Load() || q.Len() < writeQueueHighWater {
		return nil
	}
	drainSignal(w.deadlineCh)
	for {
		lowWater := q.lowWater()
		if s.IsClosed() || q.stopped() {
			return fmt.Errorf("conn.writeWindow: mux closed: %w", net.ErrClosed)
		}
		if q.Len() < writeQueueLowWater {
			return nil
		}
		timer, timeout, expired := deadlineTimer(w.deadline.Load())
		if expired {
			return os.ErrDeadlineExceeded
		}
		select {
		case <-lowWater:
		case <-s.closeChan:
		case <-w.closeOpCh:
			stopTimer(timer)
			return errWriteWindowClosed
		case <-w.deadlineCh:
		case <-timeout:
			return os.ErrDeadlineExceeded
		}
		stopTimer(timer)
	}
}

func (s *Mux) writeSession() {
	fw := NewFlushWriter(s.conn)
	go func() {
		defer func() {
			_ = fw.Flush()
			_ = fw.Close()
		}()
		for {
			if s.IsClosed() {
				return
			}
			pack := s.writeQueue.TryPop()
			if pack == nil {
				if err := fw.Flush(); err != nil {
					s.writeFailed(err)
					return
				}
				pack = s.writeQueue.Pop()
			}
			if pack == nil {
				break
			}

			flag := pack.flag

			//if pack.flag == muxNewMsg || pack.flag == muxNewMsgPart {
			//	if pack.length >= 100 {
			//		logs.Println("write session id", pack.id, "\n", string(pack.content[:100]))
			//	} else {
			//		logs.Println("write session id", pack.id, "\n", string(pack.content[:pack.length]))
			//	}
			//}

			err := pack.Pack(fw)
			muxPack.Put(pack)
			if err != nil {
				s.writeFailed(err)
				return
			}

			if flag == muxPingFlag || flag == muxPingReturn {
				if err = fw.Flush(); err != nil {
					s.writeFailed(err)
					return
				}
			}
		}
	}()
}

func (s *Mux) writeFailed(err error) {
	if s.IsClosed() {
		return
	}
	reason := fmt.Sprintf("write session pack failed: %s", connx.DescribeNetError(err, s.conn))
	logs.Error("mux: %s", reason)
	_ = s.closeWithReason(reason)
}

func (s *Mux) ping() {
	go func() {
		buf := make([]byte, 8+PingMaxPad)
		timer := time.NewTimer(nextPingDelay())
		defer timer.Stop()
		for {
			select {
			case <-s.closeChan:
				return
			case <-timer.C:
				now := time.Now()
				if s.isAliveTimeout(now) {
					reason := fmt.Sprintf("ping timeout last_alive=%s timeout=%s local=%v remote=%v",
						time.Unix(0, atomic.LoadInt64(&s.lastAliveTime)).Format(time.RFC3339Nano),
						s.pingTimeout,
						s.conn.LocalAddr(),
						s.conn.RemoteAddr(),
					)
					logs.Warn("mux: %s", reason)
					_ = s.closeWithReason(reason)
					return
				}

				binary.BigEndian.PutUint64(buf[:8], uint64(now.UnixNano()))
				pad := rand.Intn(PingMaxPad)
				s.sendInfo(muxPingFlag, muxPing, false, buf[:8+pad])

				timer.Reset(nextPingDelay())
			}
		}
	}()

	go func() {
		for {
			select {
			case pack := <-s.pingCh:
				data, _ := pack.GetContent()
				//logs.Println("mux: Ping Pack err", data, pack.length, pack.content)
				//atomic.StoreUint32(&s.pingCheckTime, 0)

				if len(data) >= 8 {
					sent := int64(binary.BigEndian.Uint64(data[:8]))
					rtt := time.Now().UnixNano() - sent
					if rtt > 0 {
						sec := float64(rtt) / 1e9
						atomic.StoreUint64(&s.latency, math.Float64bits(s.counter.Latency(sec)))
						// convert float64 to bits, store it atomic
						//if s.connType == "kcp" {
						//	logs.Println("ping", math.Float64frombits(atomic.LoadUint64(&s.latency)))
						//}
					}
				}
				windowBuff.Put(pack.content)
				muxPack.Put(pack)
			case <-s.closeChan:
				for {
					select {
					case pack := <-s.pingCh:
						windowBuff.Put(pack.content)
						muxPack.Put(pack)
					default:
						return
					}
				}
			}
		}
	}()
}

func (s *Mux) readSession() {
	go func() {
		var connection *Conn
		for {
			if s.IsClosed() {
				return
			}
			connection = s.newConnQueue.Pop()
			if connection == nil {
				return
			}
			s.connMap.Set(connection.connId, connection) //it has been Set before send ok
			select {
			case <-s.closeChan:
				// the stream may have missed the close sweep of connMap
				connection.closeLocal()
				return
			case s.newConnCh <- connection:
			}
			s.sendInfo(muxNewConnOk, connection.connId, false, nil)
		}
	}()
	go func() {
		var pack *muxPackager
		var l uint16
		var err error
		for {
			if s.IsClosed() {
				return
			}
			pack = muxPack.Get()
			s.bw.StartRead()
			if l, err = pack.UnPack(s.conn); err != nil {
				if s.IsClosed() {
					muxPack.Put(pack)
					return
				}
				reason := fmt.Sprintf("read session unpack failed: %s", connx.DescribeNetError(err, s.conn))
				logs.Error("mux: %s", reason)
				_ = s.closeWithReason(reason)
				muxPack.Put(pack)
				return
			}
			s.markAlive()
			s.bw.SetCopySize(l)
			//if pack.flag == muxNewMsg || pack.flag == muxNewMsgPart {
			//	if pack.length >= 100 {
			//		logs.Printf("read session id %d pointer %p\n%v", pack.id, pack.content, string(pack.content[:100]))
			//	} else {
			//		logs.Printf("read session id %d pointer %p\n%v", pack.id, pack.content, string(pack.content[:pack.length]))
			//	}
			//}
			switch pack.flag {
			case muxNewConn: //New connection
				connection := NewConn(pack.id, s)
				s.newConnQueue.Push(connection)
				muxPack.Put(pack)
				continue
			case muxPingFlag: //ping
				s.sendInfo(muxPingReturn, muxPing, false, pingReturnPayload(pack))
				windowBuff.Put(pack.content)
				muxPack.Put(pack)
				continue
			case muxPingReturn:
				select {
				case <-s.closeChan:
					windowBuff.Put(pack.content)
					muxPack.Put(pack)
				case s.pingCh <- pack:
				}
				continue
			default:
			}

			if connection, ok := s.connMap.Get(pack.id); ok && !connection.IsClosed() {
				switch pack.flag {
				case muxNewMsg, muxNewMsgPart: //New msg from remote connection
					err = s.newMsg(connection, pack)
					if err != nil {
						logs.Println("mux: read session connection New msg err", err)
						_ = connection.Close()
					}
					muxPack.Put(pack)
					continue
				case muxNewConnOk: //connection ok
					select {
					case connection.connStatusOkCh <- struct{}{}:
					default:
					}
					muxPack.Put(pack)
					continue
				case muxNewConnFail:
					select {
					case connection.connStatusFailCh <- struct{}{}:
					default:
					}
					muxPack.Put(pack)
					continue
				case muxMsgSendOk:
					if connection.IsClosed() {
						muxPack.Put(pack)
						continue
					}
					connection.sendWindow.SetSize(pack.window)
					muxPack.Put(pack)
					continue
				case muxConnClose: //close the connection
					connection.SetClosingFlag()
					connection.receiveWindow.Stop() // close signal to receive window
					muxPack.Put(pack)
					continue
				default:
				}
			} else if pack.flag == muxConnClose {
				muxPack.Put(pack)
				continue
			}
			muxPack.Put(pack)
		}
	}()
}

// pingReturnPayload builds the ping reply from a received ping. A ping
// carrying no padding or zero padding is answered with a freshly chosen
// zero padding; the peer only reads the first 8 bytes. Bytes beyond what was
// received are zeroed so stale pool contents never leave this side.
func pingReturnPayload(pack *muxPackager) []byte {
	buf := pack.content[:pack.length]
	if pack.length == 8 || (pack.length > 8 && isZero(buf[8:])) {
		n := 8 + rand.Intn(PingMaxPad)
		if n > maximumSegmentSize {
			n = maximumSegmentSize
		}
		if n > int(pack.length) {
			clear(pack.content[pack.length:n])
		}
		buf = pack.content[:n]
	}
	return buf
}

func isZero(buf []byte) bool {
	for _, b := range buf {
		if b != 0 {
			return false
		}
	}
	return true
}

func (s *Mux) newMsg(connection *Conn, pack *muxPackager) (err error) {
	if connection.IsClosed() {
		err = io.ErrClosedPipe
		return
	}
	//insert into queue
	if pack.flag == muxNewMsgPart {
		err = connection.receiveWindow.Write(pack.content, pack.length, true, pack.id)
	}
	if pack.flag == muxNewMsg {
		err = connection.receiveWindow.Write(pack.content, pack.length, false, pack.id)
	}
	return
}

func (s *Mux) IsClosed() bool {
	select {
	case <-s.closeChan:
		return true
	default:
		return false
	}
}

func (s *Mux) Close() (err error) {
	return s.closeWithReason("")
}

func (s *Mux) CloseReason() string {
	s.reasonMu.RLock()
	defer s.reasonMu.RUnlock()
	return s.closeReason
}

func (s *Mux) closeWithReason(reason string) (err error) {
	reason = s.setCloseReason(reason)
	//buf := make([]byte, 1024*8)
	//n := runtime.Stack(buf, false)
	//fmt.Print(string(buf[:n]))

	if s.IsClosed() {
		return fmt.Errorf("the mux has closed: %w", net.ErrClosed)
	}

	s.once.Do(func() {
		close(s.closeChan)
		if reason == "" {
			logs.Println("close mux")
		} else {
			logs.Printf("close mux, reason: %s", reason)
		}
		s.connMap.Close()
		//s.connMap = nil
		//s.closeChan <- struct{}{}
		//close(s.newConnCh)
		// while target host close socket without finish steps, conn.Close method maybe blocked
		// and tcp status change to CLOSE WAIT or TIME WAIT, so we close it in other goroutine
		_ = s.conn.SetDeadline(time.Now().Add(time.Second * 5))
		go func() {
			_ = s.conn.Close()
			_ = s.bw.Close()
		}()
		s.release()
	})
	return
}

func (s *Mux) setCloseReason(reason string) string {
	if reason == "" {
		return s.CloseReason()
	}
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.closeReason == "" {
		s.closeReason = reason
	}
	return s.closeReason
}

func (s *Mux) closedErr(prefix string) error {
	if reason := s.CloseReason(); reason != "" {
		return fmt.Errorf("%s (%s): %w", prefix, reason, net.ErrClosed)
	}
	return fmt.Errorf("%s: %w", prefix, net.ErrClosed)
}

// drainWriteQueue recycles every queued frame. Each buffer goes back to the
// pool exactly once: content here, buf in muxPack.Put via reset.
func (s *Mux) drainWriteQueue() {
	for {
		pack := s.writeQueue.drainPop()
		if pack == nil {
			return
		}
		if pack.content != nil {
			windowBuff.Put(pack.content)
			pack.content = nil
		}
		muxPack.Put(pack)
	}
}

func (s *Mux) release() {
	s.drainWriteQueue()
	for {
		connection := s.newConnQueue.TryPop()
		if connection == nil {
			break
		}
	}
	s.writeQueue.Stop()
	s.newConnQueue.Stop()
}

// Get New connId as unique flag
func (s *Mux) getId() (id int32) {
	//Avoid going beyond the scope
	curID := atomic.LoadInt32(&s.id)
	if (math.MaxInt32 - curID) < 10000 {
		if s.isInitiator {
			atomic.StoreInt32(&s.id, -1)
		} else {
			atomic.StoreInt32(&s.id, 0)
		}
	}
	id = atomic.AddInt32(&s.id, 2)
	if _, ok := s.connMap.Get(id); ok {
		return s.getId()
	}
	return
}

type Bandwidth struct {
	readBandwidth uint64 // store in bits, but it's float64; first for 64-bit atomic alignment on 32-bit platforms
	readStart     time.Time
	lastReadStart time.Time
	fd            *os.File
	bufLength     uint32
	calcThreshold uint32
}

func NewBandwidth(fd *os.File) *Bandwidth {
	return &Bandwidth{fd: fd}
}

func (Self *Bandwidth) StartRead() {
	if Self.readStart.IsZero() {
		Self.readStart = time.Now()
	}
	if Self.bufLength >= Self.calcThreshold {
		Self.lastReadStart, Self.readStart = Self.readStart, time.Now()
		Self.calcBandWidth()
	}
}

func (Self *Bandwidth) SetCopySize(n uint16) {
	Self.bufLength += uint32(n)
}

func (Self *Bandwidth) calcBandWidth() {
	t := Self.readStart.Sub(Self.lastReadStart)
	if t <= 0 {
		Self.bufLength = 0
		return
	}
	bufferSize, err := sysGetSock(Self.fd)
	if err != nil {
		logs.Println(err)
		Self.bufLength = 0
		return
	}
	if Self.bufLength >= uint32(bufferSize) {
		atomic.StoreUint64(&Self.readBandwidth, math.Float64bits(float64(Self.bufLength)/t.Seconds()))
		// calculate the whole socket buffer, the time meaning to fill the buffer
	} else {
		Self.calcThreshold = uint32(bufferSize)
	}
	// socket buffer size is bigger than bufLength, so we don't calculate it
	Self.bufLength = 0
}

func (Self *Bandwidth) Get() (bw float64) {
	// The zero value, 0 for numeric types
	bw = math.Float64frombits(atomic.LoadUint64(&Self.readBandwidth))
	if bw <= 0 {
		bw = 0
	}
	return
}

func (Self *Bandwidth) Close() error {
	if Self.fd == nil {
		return nil
	}
	return Self.fd.Close()
}

const counterBits = 4
const counterMask = 1<<counterBits - 1

func newLatencyCounter() *latencyCounter {
	return &latencyCounter{
		buf:     make([]float64, 1<<counterBits),
		headMin: 0,
	}
}

type latencyCounter struct {
	buf []float64 //buf is a fixed length ring buffer,
	// if buffer is full, New value will replace the oldest one.
	headMin uint8 //head indicate the head in ring buffer,
	// in meaning, slot in list will be replaced;
	// min indicate this slot value is minimal in list.

	// we delineate the effective range with three times the minimum latency
	// average of effective latency for all current data as a mux latency
}

func (Self *latencyCounter) unpack(idx uint8) (head, min uint8) {
	head = (idx >> counterBits) & counterMask
	// we Set head is 4 bits
	min = idx & counterMask
	return
}

func (Self *latencyCounter) pack(head, min uint8) uint8 {
	return head<<counterBits |
		min&counterMask
}

func (Self *latencyCounter) add(value float64) {
	head, minIndex := Self.unpack(Self.headMin)
	Self.buf[head] = value
	if head == minIndex {
		minIndex = Self.minimal()
		//if head equals minIndex, means the minIndex slot already be replaced,
		// so we need to find another minimal value in the list,
		// and change the minIndex indicator
	}
	if Self.buf[minIndex] <= 0 || Self.buf[minIndex] > value {
		minIndex = head
	}
	head++
	Self.headMin = Self.pack(head, minIndex)
}

func (Self *latencyCounter) minimal() (min uint8) {
	var i uint8
	var found bool
	for i = 0; i <= counterMask; i++ {
		if Self.buf[i] > 0 {
			if !found || Self.buf[i] < Self.buf[min] {
				min = i
				found = true
			}
		}
	}
	return
}

func (Self *latencyCounter) Latency(value float64) (latency float64) {
	Self.add(value)
	latency = Self.countSuccess()
	return
}

const lossRatio = 3

func (Self *latencyCounter) countSuccess() (successRate float64) {
	var i, success uint8
	_, minIndex := Self.unpack(Self.headMin)
	if Self.buf[minIndex] <= 0 {
		return 0
	}
	for i = 0; i <= counterMask; i++ {
		if Self.buf[i] <= lossRatio*Self.buf[minIndex] && Self.buf[i] > 0 {
			success++
			successRate += Self.buf[i]
		}
	}
	if success == 0 {
		return 0
	}
	// counting all the data in the ring buf, except zero
	successRate = successRate / float64(success)
	return
}
