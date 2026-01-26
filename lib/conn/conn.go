package conn

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/pmux"
	"github.com/xtaci/kcp-go/v5"
)

var LocalTCPAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}

type Conn struct {
	Conn       net.Conn
	rbs        [][]byte
	wBuf       *bytes.Buffer
	mu         sync.Mutex
	closed     uint32
	closeHooks []func(*Conn)
}

// NewConn new conn
func NewConn(conn net.Conn) *Conn {
	return &Conn{
		Conn: conn,
		wBuf: new(bytes.Buffer),
	}
}

func (s *Conn) SetRb(rbs ...[]byte) *Conn {
	for _, rb := range rbs {
		if len(rb) > 0 {
			s.rbs = append(s.rbs, rb)
		}
	}
	return s
}

func (s *Conn) OnClose(fn func(*Conn)) *Conn {
	if fn == nil {
		return s
	}
	s.closeHooks = append(s.closeHooks, fn)
	return s
}

func (s *Conn) readRequest(buf []byte) (n int, err error) {
	var rd int
	for {
		rd, err = s.Read(buf[n:])
		if err != nil {
			return
		}
		n += rd
		if n < 4 {
			continue
		}
		if string(buf[n-4:n]) == "\r\n\r\n" {
			return
		}
		// buf is full, can't contain the request
		if n == cap(buf) {
			err = io.ErrUnexpectedEOF
			return
		}
	}
}

// GetHost get host 、connection type、method...from connection
func (s *Conn) GetHost() (method, address string, rb []byte, err error, r *http.Request) {
	var b [32 * 1024]byte
	var n int
	if n, err = s.readRequest(b[:]); err != nil {
		return
	}
	rb = b[:n]
	r, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(rb)))
	if err != nil {
		return
	}
	hostPortURL, err := url.Parse(r.Host)
	if err != nil {
		address = r.Host
		err = nil
		return
	}
	if hostPortURL.Opaque == "443" {
		if strings.Index(r.Host, ":") == -1 {
			address = r.Host + ":443"
		} else {
			address = r.Host
		}
	} else {
		if strings.Index(r.Host, ":") == -1 {
			address = r.Host + ":80"
		} else {
			address = r.Host
		}
	}
	return
}

func (s *Conn) GetShortLenContent() (b []byte, err error) {
	var l int
	if l, err = s.GetLen(); err != nil {
		return
	}
	if l < 0 || l > 32<<10 {
		err = errors.New("read length error")
		return
	}
	return s.GetShortContent(l)
}

func (s *Conn) GetShortContent(l int) (b []byte, err error) {
	buf := make([]byte, l)
	return buf, binary.Read(s, binary.LittleEndian, &buf)
}

func (s *Conn) ReadLen(cLen int, buf []byte) (int, error) {
	if cLen > len(buf) || cLen <= 0 {
		return 0, errors.New("invalid length: " + strconv.Itoa(cLen))
	}
	n, err := io.ReadFull(s, buf[:cLen])
	if err != nil || n != cLen {
		return n, fmt.Errorf("error reading %d bytes: %w", cLen, err)
	}
	return cLen, nil
}

func (s *Conn) GetLen() (int, error) {
	var l int32
	err := binary.Read(s, binary.LittleEndian, &l)
	return int(l), err
}

func (s *Conn) WriteLenContent(buf []byte) (err error) {
	var b []byte
	if b, err = GetLenBytes(buf); err != nil {
		return
	}
	//return binary.Write(s.Conn, binary.LittleEndian, b)
	_, err = s.BufferWrite(b)
	return
}

// ReadFlag read flag
func (s *Conn) ReadFlag() (string, error) {
	buf := make([]byte, 4)
	return string(buf), binary.Read(s, binary.LittleEndian, &buf)
}

// SetAlive set alive
func (s *Conn) SetAlive() {
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Time{})
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Time{})
	case *pmux.PortConn:
		_ = s.Conn.(*pmux.PortConn).SetReadDeadline(time.Time{})
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Time{})
	case *TlsConn:
		_ = s.Conn.(*TlsConn).SetReadDeadline(time.Time{})
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Time{})
		}
	}
}

// SetReadDeadlineBySecond set read deadline
func (s *Conn) SetReadDeadlineBySecond(t time.Duration) {
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Now().Add(t * time.Second))
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *pmux.PortConn:
		_ = s.Conn.(*pmux.PortConn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *TlsConn:
		_ = s.Conn.(*TlsConn).SetReadDeadline(time.Now().Add(t * time.Second))
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Now().Add(t * time.Second))
		}
	}
}

// GetLinkInfo get link info from conn
func (s *Conn) GetLinkInfo() (lk *Link, err error) {
	err = s.getInfo(&lk)
	return
}

// SendHealthInfo send info for link
func (s *Conn) SendHealthInfo(info, status string) (int, error) {
	raw := bytes.NewBuffer([]byte{})
	common.BinaryWrite(raw, info, status)
	return s.Write(raw.Bytes())
}

// GetHealthInfo get health info from conn
func (s *Conn) GetHealthInfo() (info string, status bool, err error) {
	//_ = s.SetReadDeadline(time.Now().Add(timeout))
	//defer s.SetReadDeadline(time.Time{})
	var l int
	l, err = s.GetLen()
	if err != nil {
		return
	}
	buf := common.BufPoolMax.Get().([]byte)
	defer common.PutBufPoolMax(buf)
	_, err = s.ReadLen(l, buf)
	if err != nil {
		return
	}
	arr := strings.Split(string(buf[:l]), common.CONN_DATA_SEQ)
	if len(arr) < 2 {
		return "", false, errors.New("receive health info error")
	}
	return arr[0], common.GetBoolByStr(arr[1]), nil
}

// GetHostInfo get task info
func (s *Conn) GetHostInfo() (h *file.Host, err error) {
	err = s.getInfo(&h)
	h.Id = int(file.GetDb().JsonDb.GetHostId())
	h.Flow = new(file.Flow)
	h.NoStore = true
	return
}

// GetConfigInfo get task info
func (s *Conn) GetConfigInfo() (c *file.Client, err error) {
	err = s.getInfo(&c)
	c.NoStore = true
	c.Status = true
	if c.Flow == nil {
		c.Flow = new(file.Flow)
	}
	c.NoDisplay = false
	return
}

// GetTaskInfo get task info
func (s *Conn) GetTaskInfo() (t *file.Tunnel, err error) {
	err = s.getInfo(&t)
	t.Id = int(file.GetDb().JsonDb.GetTaskId())
	t.NoStore = true
	t.Flow = new(file.Flow)
	return
}

// SendInfo send  info
func (s *Conn) SendInfo(t interface{}, flag string) (int, error) {
	/*
		The task info is formed as follows:
		+----+-----+---------+
		|type| len | content |
		+----+---------------+
		| 4  |  4  |   ...   |
		+----+---------------+
	*/
	raw := bytes.NewBuffer([]byte{})
	if flag != "" {
		_ = binary.Write(raw, binary.LittleEndian, []byte(flag))
	}
	b, err := json.Marshal(t)
	if err != nil {
		return 0, err
	}
	lenBytes, err := GetLenBytes(b)
	if err != nil {
		return 0, err
	}
	_ = binary.Write(raw, binary.LittleEndian, lenBytes)
	return s.Write(raw.Bytes())
}

// get task info
func (s *Conn) getInfo(t interface{}) (err error) {
	var l int
	buf := common.BufPoolMax.Get().([]byte)
	defer common.PutBufPoolMax(buf)
	if l, err = s.GetLen(); err != nil {
		return
	} else if _, err = s.ReadLen(l, buf); err != nil {
		return
	} else {
		_ = json.Unmarshal(buf[:l], &t)
	}
	return
}

func (s *Conn) IsClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *Conn) Close() error {
	if atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		hooks := s.closeHooks
		s.closeHooks = nil
		for _, h := range hooks {
			func() {
				defer func() { _ = recover() }()
				h(s)
			}()
		}
		for i := range s.rbs {
			s.rbs[i] = nil
		}
		s.rbs = nil
		s.mu.Lock()
		if s.wBuf != nil {
			s.wBuf.Reset()
		}
		s.mu.Unlock()
		return s.Conn.Close()
	}
	return errors.New("connection already closed")
}

// write
func (s *Conn) Write(b []byte) (n int, err error) {
	if s == nil || s.IsClosed() {
		return 0, errors.New("connection error")
	}

	s.mu.Lock()
	if s.wBuf.Len() == 0 {
		s.mu.Unlock()
		return s.Conn.Write(b)
	}
	n, err = s.wBuf.Write(b)
	toSend := s.wBuf.Bytes()
	s.wBuf.Reset()
	defer s.mu.Unlock()

	if _, err := s.Conn.Write(toSend); err != nil {
		return 0, err
	}

	return
}

func (s *Conn) BufferWrite(b []byte) (int, error) {
	if s == nil || s.IsClosed() {
		return 0, errors.New("connection error")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wBuf.Write(b)
}

func (s *Conn) FlushBuf() error {
	if s.IsClosed() {
		return errors.New("connection closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf.Len() == 0 {
		return nil
	}
	_, err := s.Conn.Write(s.wBuf.Bytes())
	s.wBuf.Reset()
	return err
}

// read
func (s *Conn) Read(b []byte) (n int, err error) {
	if err = s.FlushBuf(); err != nil {
		return 0, err
	}

	for len(s.rbs) > 0 {
		cur := s.rbs[0]
		if len(cur) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			continue
		}
		n = copy(b, cur)
		s.rbs[0] = cur[n:]
		if len(s.rbs[0]) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			if len(s.rbs) == 0 {
				s.rbs = nil
			}
		}
		return n, nil
	}

	return s.Conn.Read(b)
}

// WriteClose write sign flag
func (s *Conn) WriteClose() (int, error) {
	return s.Write([]byte(common.RES_CLOSE))
}

// WriteMain write main
func (s *Conn) WriteMain() (int, error) {
	return s.Write([]byte(common.WORK_MAIN))
}

// WriteConfig write config
func (s *Conn) WriteConfig() (int, error) {
	return s.Write([]byte(common.WORK_CONFIG))
}

// WriteChan write chan
func (s *Conn) WriteChan() (int, error) {
	return s.Write([]byte(common.WORK_CHAN))
}

// GetAddStatus get task or host result of add
func (s *Conn) GetAddStatus() (b bool) {
	_ = binary.Read(s, binary.LittleEndian, &b)
	return
}

func (s *Conn) WriteAddOk() error {
	return binary.Write(s, binary.LittleEndian, true)
}

func (s *Conn) WriteAddFail() error {
	defer s.Close()
	return binary.Write(s, binary.LittleEndian, false)
}

func (s *Conn) LocalAddr() net.Addr {
	return s.Conn.LocalAddr()
}

func (s *Conn) RemoteAddr() net.Addr {
	return s.Conn.RemoteAddr()
}

func (s *Conn) SetDeadline(t time.Time) error {
	return s.Conn.SetDeadline(t)
}

func (s *Conn) SetWriteDeadline(t time.Time) error {
	return s.Conn.SetWriteDeadline(t)
}

func (s *Conn) SetReadDeadline(t time.Time) error {
	return s.Conn.SetReadDeadline(t)
}
