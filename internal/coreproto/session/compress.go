package session

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/internal/coreproto/wire"
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
)

// Compression of a stream (§7).
const (
	// DefaultCompressMaxWindow is compress_max_window (§7.4).
	DefaultCompressMaxWindow = 8 << 20
	// DefaultCompressMaxRatio is compress_max_ratio (§7.4).
	DefaultCompressMaxRatio = 4096

	// compressAllowance is the output a stream may have before the ratio
	// applies (§7.4).
	compressAllowance = 16 << 20
	// maxDecompressRead is the most one Read hands to the application (§7.4).
	maxDecompressRead = 64 << 10
	// zstdSendWindow is below the 8 MiB the format allows a sender: every
	// open stream holds one on both sides.
	zstdSendWindow = 1 << 20
	// compressFinishTimeout bounds writing the end of a compressed stream.
	compressFinishTimeout = time.Second
)

// CompressLimits are the receive limits of §7.4.
type CompressLimits struct {
	MaxWindow uint64 // 0 is DefaultCompressMaxWindow
	MaxRatio  int64  // 0 is DefaultCompressMaxRatio, negative disables the check
}

func (l CompressLimits) normalized() CompressLimits {
	if l.MaxWindow == 0 {
		l.MaxWindow = DefaultCompressMaxWindow
	}
	if l.MaxRatio == 0 {
		l.MaxRatio = DefaultCompressMaxRatio
	}
	return l
}

// errCompress marks data that is invalid, truncated or over a limit.
var errCompress = errors.New("compressed stream invalid")

var compressNames = map[string]uint64{
	"flate":  wire.CompressFlate,
	"zstd":   wire.CompressZstd,
	"snappy": wire.CompressSnappy,
}

// encoder is the sending side of one algorithm.
type encoder interface {
	io.Writer
	Flush() error
	Close() error
	Reset(io.Writer)
}

type flateEncoder struct{ *flate.Writer }

func (e flateEncoder) Flush() error      { return e.Writer.Flush() }
func (e flateEncoder) Reset(w io.Writer) { e.Writer.Reset(w) }

func newEncoder(alg uint64) (encoder, error) {
	switch alg {
	case wire.CompressFlate:
		w, err := flate.NewWriter(io.Discard, flate.BestSpeed)
		if err != nil {
			return nil, err
		}
		return flateEncoder{w}, nil
	case wire.CompressZstd:
		return zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithEncoderConcurrency(1),
			zstd.WithZeroFrames(true),
			zstd.WithLowerEncoderMem(true),
			zstd.WithWindowSize(zstdSendWindow))
	case wire.CompressSnappy:
		return snappy.NewBufferedWriter(io.Discard), nil
	}
	return nil, fmt.Errorf("compress: unknown algorithm %d", alg)
}

// encoderPools keep the encoders of closed streams, by algorithm: building
// one costs far more than a stream's first bytes.
var encoderPools = map[uint64]*sync.Pool{
	wire.CompressFlate:  {},
	wire.CompressZstd:   {},
	wire.CompressSnappy: {},
}

func getEncoder(alg uint64, w io.Writer) (encoder, error) {
	if e, ok := encoderPools[alg].Get().(encoder); ok {
		e.Reset(w)
		return e, nil
	}
	e, err := newEncoder(alg)
	if err == nil {
		e.Reset(w)
	}
	return e, err
}

func putEncoder(alg uint64, e encoder) {
	e.Reset(nil)
	encoderPools[alg].Put(e)
}

// countReader counts what the decoder took from the stream and keeps the
// stream's own error apart from the decoder's verdicts.
type countReader struct {
	r   io.Reader
	n   uint64
	err error // the first error of r other than EOF
	eof bool
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += uint64(n)
	if errors.Is(err, io.EOF) {
		c.eof = true
	} else if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// compressConn is a stream carrying one compressed stream in each direction
// (§7.3). The stream is the link's: only compressConn's methods reach it, so
// that nothing writes around the compressor.
type compressConn struct {
	c      *linkConn
	alg    uint64
	limits CompressLimits

	wmu  sync.Mutex
	enc  encoder
	bw   *bufio.Writer
	wfin bool
	werr error

	src     *countReader
	br      *bufio.Reader
	dec     io.Reader
	out     uint64
	rerr    error
	started bool
}

// newCompressConn wraps c; the reader side is built on the first Read so that
// a want_reply head before it stays plain (§7.3).
func newCompressConn(c *linkConn, alg uint64, limits CompressLimits) (*compressConn, error) {
	cc := &compressConn{c: c, alg: alg, limits: limits.normalized()}
	cc.bw = bufio.NewWriterSize(c, 16<<10)
	e, err := getEncoder(alg, cc.bw)
	if err != nil {
		return nil, err
	}
	cc.enc = e
	return cc, nil
}

func (c *compressConn) startRead() error {
	c.started = true
	c.src = &countReader{r: c.c}
	c.br = bufio.NewReaderSize(c.src, 16<<10)
	var err error
	switch c.alg {
	case wire.CompressFlate:
		c.dec = flate.NewReader(c.br)
	case wire.CompressZstd:
		var d *zstd.Decoder
		d, err = zstd.NewReader(c.br,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(c.limits.MaxWindow))
		c.dec = d
	case wire.CompressSnappy:
		c.dec = snappy.NewReader(c.br)
	default:
		err = errCompress
	}
	return err
}

func (c *compressConn) Read(p []byte) (int, error) {
	if c.rerr != nil {
		return 0, c.rerr
	}
	if len(p) == 0 {
		return 0, nil
	}
	if !c.started {
		if err := c.startRead(); err != nil {
			return 0, c.fail(err)
		}
	}
	n, err := c.dec.Read(p[:min(len(p), maxDecompressRead)])
	c.out += uint64(n)
	if c.overRatio() {
		return 0, c.fail(fmt.Errorf("%w: ratio limit", errCompress))
	}
	switch {
	case err == nil:
		return n, nil
	case err == io.EOF:
		if ferr := c.finishRead(); ferr != nil {
			return n, ferr
		}
		c.rerr = io.EOF
		return n, io.EOF
	case c.src.err != nil:
		// The stream failed under the decoder: its error is the news.
		c.rerr = c.src.err
		return n, c.src.err
	}
	return n, c.fail(fmt.Errorf("%w: %v", errCompress, err))
}

// finishRead checks what the end of the decoded data requires (§7.3).
func (c *compressConn) finishRead() error {
	switch c.alg {
	case wire.CompressZstd:
		// A stream of no frames is not an empty stream: the sender writes
		// an empty frame for that.
		if c.src.n == 0 {
			return c.fail(fmt.Errorf("%w: no frame", errCompress))
		}
	case wire.CompressFlate:
		// Nothing may follow the final block.
		switch _, err := c.br.ReadByte(); {
		case err == nil:
			return c.fail(fmt.Errorf("%w: data after the final block", errCompress))
		case !errors.Is(err, io.EOF):
			c.rerr = err
			return err
		}
	}
	return nil
}

func (c *compressConn) overRatio() bool {
	r := c.limits.MaxRatio
	if r < 0 || c.out <= compressAllowance {
		return false
	}
	return (c.out-compressAllowance-1)/uint64(r) >= c.src.n
}

// fail resets the stream with NPS_COMPRESS_ERROR; the session stays (§7.4).
func (c *compressConn) fail(err error) error {
	c.rerr = err
	c.c.abort(wire.CodeCompressError)
	return err
}

func (c *compressConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.wfin {
		return 0, net.ErrClosed
	}
	if c.werr != nil {
		return 0, c.werr
	}
	n, err := c.enc.Write(p)
	if err == nil {
		err = c.flush()
	}
	if err != nil {
		c.werr = err
	}
	return n, err
}

func (c *compressConn) flush() error {
	if err := c.enc.Flush(); err != nil {
		return err
	}
	return c.bw.Flush()
}

// CloseWrite ends the compressed stream of this direction and sends FIN; the
// other direction goes on.
func (c *compressConn) CloseWrite() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.finishWrite()
}

// finishWrite writes the end of the compressed stream; the caller holds wmu.
func (c *compressConn) finishWrite() error {
	if c.wfin {
		return nil
	}
	c.wfin = true
	if c.werr != nil {
		return c.werr
	}
	_ = c.c.SetWriteDeadline(time.Now().Add(compressFinishTimeout))
	err := c.enc.Close()
	if err == nil {
		err = c.bw.Flush()
	}
	if err != nil {
		c.werr = err
		return err
	}
	putEncoder(c.alg, c.enc)
	c.enc = nil
	return c.c.CloseWrite()
}

// Close ends both directions. A Write still in progress is stuck on a peer
// that does not read, and then the stream is reset rather than finished.
func (c *compressConn) Close() error {
	if !c.wmu.TryLock() {
		c.c.abort(uint64(streamCancelled))
		return nil
	}
	defer c.wmu.Unlock()
	if c.werr != nil {
		c.wfin = true
		c.c.abort(uint64(streamCancelled))
		return c.werr
	}
	err := c.finishWrite()
	_ = c.c.Close()
	return err
}

func (c *compressConn) LocalAddr() net.Addr               { return c.c.LocalAddr() }
func (c *compressConn) RemoteAddr() net.Addr              { return c.c.RemoteAddr() }
func (c *compressConn) SetDeadline(t time.Time) error     { return c.c.SetDeadline(t) }
func (c *compressConn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }
func (c *compressConn) SetWriteDeadline(t time.Time) error {
	return c.c.SetWriteDeadline(t)
}
