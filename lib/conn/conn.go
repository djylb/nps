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
	hookMu     sync.Mutex
	closeHooks []func(*Conn)
}

// NewConn new conn
func NewConn(conn net.Conn) *Conn {
	return &Conn{
		Conn: conn,
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
	s.hookMu.Lock()
	if !s.IsClosed() {
		s.closeHooks = append(s.closeHooks, fn)
		s.hookMu.Unlock()
		return s
	}
	s.hookMu.Unlock()
	// already closed: Close has taken its hooks, so run this one now
	runCloseHook(fn, s)
	return s
}

func runCloseHook(fn func(*Conn), c *Conn) {
	defer func() { _ = recover() }()
	fn(c)
}

// GetHost reads one HTTP proxy request header from the connection and resolves
// the target address. head holds the raw header bytes and rest holds everything
// read past the header (request body, pipelined requests or early tunnel data)
// followed by any data still queued on the connection, so nothing stays queued
// on return. On error head holds every byte consumed so far.
func (s *Conn) GetHost() (address string, r *http.Request, head, rest []byte, err error) {
	var raw bytes.Buffer
	br := bufio.NewReader(io.TeeReader(io.LimitReader(s, http.DefaultMaxHeaderBytes), &raw))
	r, err = http.ReadRequest(br)
	if err != nil {
		head = raw.Bytes()
		return
	}
	b := raw.Bytes()
	n := len(b) - br.Buffered()
	head = b[:n:n]
	if len(b) > n {
		rest = b[n:]
	}
	for _, rb := range s.rbs {
		rest = append(rest, rb...)
	}
	s.rbs = nil
	address = requestTargetAddress(r)
	return
}

// requestTargetAddress returns host:port for a proxy request, adding the
// scheme's default port when the request names none.
func requestTargetAddress(r *http.Request) string {
	if _, _, err := net.SplitHostPort(r.Host); err == nil {
		return r.Host
	}
	port := "80"
	if r.Method == http.MethodConnect || strings.EqualFold(r.URL.Scheme, "https") {
		port = "443"
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(r.Host, "["), "]"), port)
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
	var buf [4]byte
	_, err := io.ReadFull(s, buf[:])
	return string(buf[:]), err
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
	var raw bytes.Buffer
	common.BinaryWrite(&raw, info, status)
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
	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)
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
	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)
	if l, err = s.GetLen(); err != nil {
		return
	}
	if _, err = s.ReadLen(l, buf); err != nil {
		return
	}
	_ = json.Unmarshal(buf[:l], &t)
	return
}

func (s *Conn) IsClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *Conn) Close() error {
	if atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		s.hookMu.Lock()
		hooks := s.closeHooks
		s.closeHooks = nil
		s.hookMu.Unlock()
		for _, h := range hooks {
			runCloseHook(h, s)
		}
		// rbs is left alone: a Read running concurrently, as on any conn
		// closed by another goroutine, may still be looking at it.
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
	defer s.mu.Unlock()
	if s.wBuf == nil || s.wBuf.Len() == 0 {
		return s.Conn.Write(b)
	}
	n, err = s.wBuf.Write(b)
	toSend := s.wBuf.Bytes()
	s.wBuf.Reset()

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
	if s.wBuf == nil {
		s.wBuf = new(bytes.Buffer)
	}
	return s.wBuf.Write(b)
}

func (s *Conn) FlushBuf() error {
	if s.IsClosed() {
		return errors.New("connection closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil || s.wBuf.Len() == 0 {
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
	defer func() { _ = s.Close() }()
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
