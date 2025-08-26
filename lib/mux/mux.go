package mux

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

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

type Mux struct {
	latency uint64 // we store latency in bits, but it's float64
	net.Listener
	conn               net.Conn
	connMap            *ConnMap
	newConnCh          chan *Conn
	id                 int32
	isInitiator        bool
	closeChan          chan struct{}
	counter            *latencyCounter
	bw                 *Bandwidth
	pingCh             chan *muxPackager
	pingCheckTime      uint32 // we check the ping per 5s
	pingCheckThreshold uint32
	connType           string
	writeQueue         priorityQueue
	newConnQueue       connQueue
	once               sync.Once
}

func NewMux(c net.Conn, connType string, pingCheckThreshold int, isInitiator bool) *Mux {
	//c.(*net.TCPConn).SetReadBuffer(0)
	//c.(*net.TCPConn).SetWriteBuffer(0)
	fd, err := getConnFd(c)
	if err != nil {
		logs.Println(err)
	}
	var checkThreshold uint32
	if pingCheckThreshold <= 0 {
		if connType == "kcp" {
			checkThreshold = 20
		} else {
			checkThreshold = 60
		}
	} else {
		checkThreshold = uint32(pingCheckThreshold)
	}
	var startId int32
	if isInitiator {
		startId = -1
	} else {
		startId = 0
	}
	m := &Mux{
		conn:               c,
		connMap:            NewConnMap(),
		id:                 startId,
		closeChan:          make(chan struct{}, 1),
		newConnCh:          make(chan *Conn),
		bw:                 NewBandwidth(fd),
		connType:           connType,
		pingCh:             make(chan *muxPackager),
		pingCheckThreshold: checkThreshold,
		counter:            newLatencyCounter(),
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

func (s *Mux) NewConn() (*Conn, error) {
	if s.IsClosed() {
		return nil, errors.New("the mux has closed")
	}
	conn := NewConn(s.getId(), s)
	//it must be Set before send
	s.connMap.Set(conn.connId, conn)
	s.sendInfo(muxNewConn, conn.connId, false, nil)
	//Set a timer timeout 120 second
	timer := time.NewTimer(time.Minute * 2)
	defer timer.Stop()
	select {
	case <-conn.connStatusOkCh:
		return conn, nil
	case <-timer.C:
		s.connMap.Delete(conn.connId)
	}
	return nil, errors.New("create connection fail, the server refused the connection")
}

func (s *Mux) Accept() (net.Conn, error) {
	select {
	case <-s.closeChan:
		return nil, errors.New("accept error: the mux has closed")
	case conn, ok := <-s.newConnCh:
		if !ok || conn == nil {
			return nil, errors.New("accept error: the connection has been closed")
		}
		return conn, nil
	}
}

func (s *Mux) Addr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *Mux) sendInfo(flag uint8, id int32, priority bool, data interface{}) {
	if s.IsClosed() {
		return
	}
	pack := muxPack.Get()
	pack.priority = priority
	if err := pack.Set(flag, id, data); err != nil {
		muxPack.Put(pack)
		logs.Println("mux: New Pack err", err)
		_ = s.Close()
		return
	}
	s.writeQueue.Push(pack)
	return
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
				break
			}
			pack := s.writeQueue.TryPop()
			if pack == nil {
				_ = fw.Flush()
				pack = s.writeQueue.Pop()
			}
			if pack == nil {
				break
			}
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
				logs.Println("mux: Pack err", err)
				_ = s.Close()
				break
			}
		}
	}()
}

func (s *Mux) ping() {
	go func() {
		rand.Seed(time.Now().UnixNano())
		buf := make([]byte, 8+PingMaxPad)
		initialJitter := time.Duration(rand.Int63n(int64(PingJitter))) - PingJitter/2
		timer := time.NewTimer(PingInterval + initialJitter)
		defer timer.Stop()
		for {
			select {
			case <-s.closeChan:
				return
			case <-timer.C:
				if atomic.LoadUint32(&s.pingCheckTime) > s.pingCheckThreshold {
					logs.Println("mux: ping timeout, check-time", s.pingCheckTime, "threshold", s.pingCheckThreshold)
					_ = s.Close()
					// more than limit times not receive the ping return package,
					// mux conn is damaged, maybe a packet drop, close it
					return
				}

				binary.BigEndian.PutUint64(buf[:8], uint64(time.Now().UnixNano()))
				pad := rand.Intn(PingMaxPad)
				payload := buf[:8+pad]

				s.sendInfo(muxPingFlag, muxPing, false, payload)
				atomic.AddUint32(&s.pingCheckTime, 1)

				jitter := time.Duration(rand.Int63n(int64(PingJitter))) - PingJitter/2
				next := PingInterval + jitter
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(next)
			}
		}
	}()

	go func() {
		for {
			select {
			case pack := <-s.pingCh:
				data, _ := pack.GetContent()
				//logs.Println("mux: Ping Pack err", data, pack.length, pack.content)
				atomic.StoreUint32(&s.pingCheckTime, 0)
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
				logs.Println("mux: read session unpack from connection err", err)
				_ = s.Close()
				muxPack.Put(pack)
				return
			}
			atomic.StoreUint32(&s.pingCheckTime, 0)
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
				buf := pack.content[:pack.length]
				if pack.length == 8 || (pack.length > 8 && isZero(buf[8:])) {
					pad := rand.Intn(PingMaxPad)
					buf = pack.content[:8+pad]
				}
				s.sendInfo(muxPingReturn, muxPing, false, buf)
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
					connection.connStatusOkCh <- struct{}{}
					muxPack.Put(pack)
					continue
				case muxNewConnFail:
					connection.connStatusFailCh <- struct{}{}
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
	//buf := make([]byte, 1024*8)
	//n := runtime.Stack(buf, false)
	//fmt.Print(string(buf[:n]))

	if s.IsClosed() {
		return errors.New("the mux has closed")
	}

	s.once.Do(func() {
		close(s.closeChan)
		logs.Println("close mux")
		s.connMap.Close()
		//s.connMap = nil
		//s.closeChan <- struct{}{}
		close(s.newConnCh)
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

func (s *Mux) release() {
	for {
		pack := s.writeQueue.TryPop()
		if pack == nil {
			break
		}
		if pack.basePackager.buf != nil {
			windowBuff.Put(pack.basePackager.buf)
		}
		if pack.basePackager.content != nil {
			windowBuff.Put(pack.basePackager.content)
		}
		muxPack.Put(pack)
	}
	for {
		connection := s.newConnQueue.TryPop()
		if connection == nil {
			break
		}
		connection = nil
	}
	s.writeQueue.Stop()
	s.newConnQueue.Stop()
}

// Get New connId as unique flag
func (s *Mux) getId() (id int32) {
	//Avoid going beyond the scope
	if (math.MaxInt32 - s.id) < 10000 {
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
	readBandwidth uint64 // store in bits, but it's float64
	readStart     time.Time
	lastReadStart time.Time
	bufLength     uint32
	fd            *os.File
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
	if Self.buf[minIndex] > value {
		minIndex = head
	}
	head++
	Self.headMin = Self.pack(head, minIndex)
}

func (Self *latencyCounter) minimal() (min uint8) {
	var val float64
	var i uint8
	for i = 0; i < counterMask; i++ {
		if Self.buf[i] > 0 {
			if val > Self.buf[i] {
				val = Self.buf[i]
				min = i
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
	for i = 0; i < counterMask; i++ {
		if Self.buf[i] <= lossRatio*Self.buf[minIndex] && Self.buf[i] > 0 {
			success++
			successRate += Self.buf[i]
		}
	}
	// counting all the data in the ring buf, except zero
	successRate = successRate / float64(success)
	return
}
