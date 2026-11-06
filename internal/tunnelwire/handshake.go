package tunnelwire

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"time"
)

var (
	// ErrProbe is a peer that left before saying what it speaks.
	ErrProbe = errors.New("connection closed before the version")
	// ErrVersion is a version this server does not accept.
	ErrVersion = errors.New("basic version mismatch")
	// ErrUnauthorized is a key the lookup does not know, or a bad MAC.
	ErrUnauthorized = errors.New("validation failed")
	// ErrDisabled is a key of a disabled client.
	ErrDisabled = errors.New("client is disabled")
	// ErrVersionMismatch: the server's proof does not match the client's version.
	ErrVersionMismatch = errors.New("the client does not match the server version")
)

// bufConn buffers writes until the next read or flush, so that a side's turn
// goes out as one write.
type bufConn struct {
	rw  io.ReadWriter
	buf bytes.Buffer
}

func (c *bufConn) Read(p []byte) (int, error) {
	if err := c.flush(); err != nil {
		return 0, err
	}
	return c.rw.Read(p)
}

func (c *bufConn) put(b []byte)      { c.buf.Write(b) }
func (c *bufConn) putFrame(b []byte) { c.buf.Write(AppendFrame(nil, b)) }

func (c *bufConn) flush() error {
	if c.buf.Len() == 0 {
		return nil
	}
	_, err := c.rw.Write(c.buf.Bytes())
	c.buf.Reset()
	return err
}

// Credential is what a server knows of a client key.
type Credential struct {
	ID      int
	Key     string
	Enabled bool
}

// ServerConfig is what ServerHandshake needs from its server.
type ServerConfig struct {
	// Secure refuses old versions, checks the time and the MAC, stays silent
	// on a bad key.
	Secure bool
	// LookupDigest finds the client of a KeyDigest.
	LookupDigest func(digest string) (Credential, error)
	// LookupMD5 finds the client of the MD5Hex of its key, for index 0.
	LookupMD5 func(digest string) (Credential, error)
	// CheckTime, when Secure, rejects a timestamp before the MAC is verified.
	CheckTime func(ts int64) error
	// CheckReplay, when Secure, rejects a timestamp and verified MAC seen before.
	CheckReplay func(ts int64, mac []byte) error
	// Fingerprint returns the fingerprint of the server's certificate.
	Fingerprint func() []byte
	// NewUUID returns a new UUID to send as the server's.
	NewUUID func() string
}

// ServerResult describes a verified client.
type ServerResult struct {
	Caps     Capabilities
	ClientID int
	// LocalIP and Transport are what the client told; zero at index 0 and
	// Transport before TransportInInfo.
	LocalIP   net.IP
	Transport string
}

// ServerHandshake verifies the client on rw, which has already had its deadline
// set. A bad key is answered as cfg asks; the caller closes rw on any error.
func ServerHandshake(rw io.ReadWriter, cfg ServerConfig) (*ServerResult, error) {
	c := &bufConn{rw: rw}
	if _, err := ReadExact(c, len(Magic)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProbe, err)
	}
	minVer, err := ReadFrame(c)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProbe, err)
	}
	index := Index(string(minVer))
	if index == -1 || (cfg.Secure && index < MinSecureIndex) {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrVersion, Version(LatestIndex), minVer)
	}
	padded, err := ReadFrame(c)
	if err != nil {
		return nil, fmt.Errorf("read client version: %w", err)
	}
	caps := Caps(index, string(bytes.TrimRight(padded, "\x00")))
	res := &ServerResult{Caps: caps}
	if !caps.TimestampAuth {
		return res, serverMD5(c, rw, cfg, res)
	}

	tsBuf, err := ReadExact(c, 8)
	if err != nil {
		return nil, fmt.Errorf("read timestamp: %w", err)
	}
	ts := int64(binary.BigEndian.Uint64(tsBuf))
	if cfg.Secure && cfg.CheckTime != nil {
		if err := cfg.CheckTime(ts); err != nil {
			return nil, fmt.Errorf("timestamp %d: %w", ts, err)
		}
	}
	digest, err := ReadExact(c, 64)
	if err != nil {
		return nil, fmt.Errorf("read key digest: %w", err)
	}
	cred, err := cfg.LookupDigest(string(digest))
	if err != nil {
		refuse(rw, cfg.Secure)
		return nil, fmt.Errorf("%w: key %x: %w", ErrUnauthorized, digest, err)
	}
	if !cred.Enabled {
		return nil, fmt.Errorf("%w: client %d", ErrDisabled, cred.ID)
	}
	res.ClientID = cred.ID
	infoBuf, err := ReadFrame(c)
	if err != nil {
		return nil, fmt.Errorf("read info: %w", err)
	}
	info, err := Open(infoBuf, cred.Key)
	if err != nil {
		return nil, fmt.Errorf("decrypt info: %w", err)
	}
	if err := parseInfo(info, res); err != nil {
		return nil, err
	}
	randBuf, err := ReadFrame(c)
	if err != nil {
		return nil, fmt.Errorf("read padding: %w", err)
	}
	mac, err := ReadExact(c, 32)
	if err != nil {
		return nil, fmt.Errorf("read mac: %w", err)
	}
	if cfg.Secure && subtle.ConstantTimeCompare(mac, handshakeMAC(cred.Key, ts, minVer, padded, infoBuf, randBuf)) != 1 {
		return nil, fmt.Errorf("%w: bad mac", ErrUnauthorized)
	}
	if cfg.Secure && cfg.CheckReplay != nil {
		if err := cfg.CheckReplay(ts, mac); err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
	}
	c.put(handshakeMAC(cred.Key, ts, mac, []byte(Version(index))))
	if caps.CertFingerprint {
		fp, err := Seal(cfg.Fingerprint(), cred.Key)
		if err != nil {
			return nil, fmt.Errorf("encrypt fingerprint: %w", err)
		}
		c.putFrame(fp)
	}
	if caps.NodeID {
		c.putFrame([]byte(cfg.NewUUID()))
	}
	if caps.RandomPadding {
		pad, err := randomPadding()
		if err != nil {
			return nil, err
		}
		c.putFrame(pad)
	}
	if err := c.flush(); err != nil {
		return nil, fmt.Errorf("write response: %w", err)
	}
	return res, nil
}

// serverMD5 is the rest of a handshake at index 0.
func serverMD5(c *bufConn, w io.Writer, cfg ServerConfig, res *ServerResult) error {
	if _, err := w.Write([]byte(MD5Hex(Version(res.Caps.Index)))); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	digest, err := ReadExact(c, 32)
	if err != nil {
		return fmt.Errorf("read key: %w", err)
	}
	cred, err := cfg.LookupMD5(string(digest))
	if err != nil {
		refuse(w, cfg.Secure)
		return fmt.Errorf("%w: key %x: %w", ErrUnauthorized, digest, err)
	}
	res.ClientID = cred.ID
	if _, err := w.Write([]byte(VerifySuccess)); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}

func refuse(w io.Writer, secure bool) {
	if !secure {
		_, _ = w.Write([]byte(VerifyFailed))
	}
}

// infoIPLen is the length of an encoded address: a type and 16 bytes.
const infoIPLen = 17

// parseInfo reads the client's info: its address, then at TransportInInfo a
// length byte and its transport.
func parseInfo(info []byte, res *ServerResult) error {
	if !res.Caps.TransportInInfo {
		res.LocalIP = decodeIP(info)
		return nil
	}
	if len(info) < infoIPLen+1 {
		return fmt.Errorf("invalid info length %d", len(info))
	}
	l := int(info[infoIPLen])
	if len(info) < infoIPLen+1+l {
		return fmt.Errorf("declared transport length %d exceeds info", l)
	}
	if res.LocalIP = decodeIP(info[:infoIPLen]); res.LocalIP == nil {
		return errors.New("cannot decode the address in the info")
	}
	res.Transport = string(info[infoIPLen+1 : infoIPLen+1+l])
	return nil
}

func encodeIP(ip net.IP) []byte {
	buf := make([]byte, infoIPLen)
	if ip4 := ip.To4(); ip4 != nil {
		buf[0] = 0x01
		copy(buf[1:], ip4)
	} else {
		buf[0] = 0x04
		copy(buf[1:], ip.To16())
	}
	return buf
}

func decodeIP(data []byte) net.IP {
	if len(data) < infoIPLen {
		return nil
	}
	addr := data[1:infoIPLen]
	switch data[0] {
	case 0x01:
		return net.IPv4(addr[0], addr[1], addr[2], addr[3])
	case 0x04:
		return addr
	}
	return nil
}

// maxTransportName bounds the transport name in the info.
const maxTransportName = 32

// maxVersionPad bounds the zero bytes after the client version.
const maxVersionPad = 64

// ClientConfig is what ClientHandshake sends and how it checks the server.
type ClientConfig struct {
	Index         int
	ClientVersion string
	Key           string
	Transport     string
	LocalIP       net.IP
	// Timestamp returns the time to authenticate with, in Unix seconds; the
	// default is the clock, up to 5 seconds back.
	Timestamp func() int64
	// PadLen returns how many zero bytes follow the client version; the
	// default is random.
	PadLen func() int
	// VerifyFingerprint, if set, may reject the server's fingerprint.
	VerifyFingerprint func(fingerprint []byte) error
}

// ClientResult is what a verified server told.
type ClientResult struct {
	Caps Capabilities
	// ServerUUID is empty before NodeID.
	ServerUUID string
	// Fingerprint is nil before CertFingerprint.
	Fingerprint []byte
	// VersionWarning: at index 0, the server's version differs; the old
	// protocol carries on regardless.
	VersionWarning bool
}

// ClientHandshake verifies rw's server, and proves the key to it. The caller
// sets the deadline and closes rw on any error.
func ClientHandshake(rw io.ReadWriter, cfg ClientConfig) (*ClientResult, error) {
	caps := Caps(cfg.Index, cfg.ClientVersion)
	res := &ClientResult{Caps: caps}
	c := &bufConn{rw: rw}
	coreVersion := []byte(Version(cfg.Index))
	c.put([]byte(Magic))
	c.putFrame(coreVersion)
	padLen := rand.IntN(maxVersionPad)
	if cfg.PadLen != nil {
		padLen = cfg.PadLen()
	}
	padded := append([]byte(cfg.ClientVersion), make([]byte, padLen)...)
	c.putFrame(padded)
	if !caps.TimestampAuth {
		return res, clientMD5(c, cfg, res)
	}

	ts := time.Now().Unix() - int64(rand.IntN(6))
	if cfg.Timestamp != nil {
		ts = cfg.Timestamp()
	}
	c.put(binary.BigEndian.AppendUint64(nil, uint64(ts)))
	c.put([]byte(KeyDigest(cfg.Key)))
	info := encodeIP(cfg.LocalIP)
	if caps.TransportInInfo {
		if len(cfg.Transport) > maxTransportName {
			return nil, fmt.Errorf("tp too long: %d bytes (max %d)", len(cfg.Transport), maxTransportName)
		}
		info = append(info, byte(len(cfg.Transport)))
		info = append(info, cfg.Transport...)
	}
	infoBuf, err := Seal(info, cfg.Key)
	if err != nil {
		return nil, err
	}
	c.putFrame(infoBuf)
	randBuf, err := randomPadding()
	if err != nil {
		return nil, err
	}
	c.putFrame(randBuf)
	mac := handshakeMAC(cfg.Key, ts, coreVersion, padded, infoBuf, randBuf)
	c.put(mac)
	proof, err := ReadExact(c, 32)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthorized, err)
	}
	if !bytes.Equal(proof, handshakeMAC(cfg.Key, ts, mac, coreVersion)) {
		return nil, fmt.Errorf("%w %s", ErrVersionMismatch, coreVersion)
	}
	if caps.CertFingerprint {
		fpBuf, err := ReadFrame(c)
		if err != nil {
			return nil, err
		}
		if res.Fingerprint, err = Open(fpBuf, cfg.Key); err != nil {
			return nil, err
		}
		if cfg.VerifyFingerprint != nil {
			if err := cfg.VerifyFingerprint(res.Fingerprint); err != nil {
				return nil, err
			}
		}
	}
	if caps.NodeID {
		b, err := ReadFrame(c)
		if err != nil {
			return nil, err
		}
		res.ServerUUID = string(b)
	}
	if caps.RandomPadding {
		if _, err := ReadFrame(c); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func clientMD5(c *bufConn, cfg ClientConfig, res *ClientResult) error {
	b, err := ReadExact(c, 32)
	if err != nil {
		return err
	}
	res.VersionWarning = MD5Hex(Version(cfg.Index)) != string(b)
	c.put([]byte(MD5Hex(cfg.Key)))
	status, err := ReadFlag(c)
	if err != nil {
		return err
	}
	if status == VerifyFailed {
		return ErrUnauthorized
	}
	return nil
}
