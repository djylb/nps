package tunnelwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Flags name what a connection or message is for.
const (
	Magic = "TST" // the first bytes a client sends
	Ack   = "ACK" // the answer to a link that asks for one

	VerifyFailed  = "vkey" // index 0: the key is wrong
	VerifySuccess = "sucs" // index 0: the key is right

	WorkMain     = "main"
	WorkChan     = "chan"
	WorkVisitor  = "vstr"
	WorkConfig   = "conf"
	WorkRegister = "rgst"
	WorkSecret   = "sert" // point-to-point access of npc before 0.35, refused
	// WorkSecretAttach proves a secret task's password once and holds the
	// grant it gets; WorkSecretLink is one relayed link under a grant.
	WorkSecretAttach = "seca"
	WorkSecretLink   = "secl"
	WorkFile         = "file"
	WorkP2P          = "p2pm"
	WorkP2PSession   = "p2sj"
	WorkStatus       = "stus"

	ResClose       = "clse"
	P2PPunchStart  = "p2st" // a P2P invitation on the signal connection
	P2PRevokeGrant = "p2rv" // a grant's direct paths end, on the signal connection
	UpdateRequest  = "updt" // an update request on the signal connection
	HealthSet      = "hset" // targets added to a health group on the signal connection
	HealthDel      = "hdel" // a health group removed on the signal connection

	FlagNewTask = "task"
	FlagNewConf = "conf"
	FlagNewHost = "host"

	// DataSeparator ends each field of a health message and of a status list.
	DataSeparator = "*#*"
)

const (
	// MaxShortFrame bounds the frames of handshakes and work requests.
	MaxShortFrame = 32 << 10
	// MaxInfoFrame bounds the frames holding a JSON message or a health report.
	MaxInfoFrame = 64 << 10
	// flagLen is the length of every flag.
	flagLen = 4
	// paddingMax bounds the random frames.
	paddingMax = 1000
)

// ErrFrameLength is a frame whose length prefix is out of range.
var ErrFrameLength = errors.New("read length error")

// ReadExact reads n bytes.
func ReadExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return buf, err
	}
	return buf, nil
}

// ReadLen reads a frame's length prefix.
func ReadLen(r io.Reader) (int, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int(int32(binary.LittleEndian.Uint32(b[:]))), nil
}

// ReadFrame reads a length-prefixed frame of at most MaxShortFrame bytes.
func ReadFrame(r io.Reader) ([]byte, error) {
	n, err := ReadLen(r)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > MaxShortFrame {
		return nil, ErrFrameLength
	}
	return ReadExact(r, n)
}

// ReadInfoFrame reads a length-prefixed frame of 1 to MaxInfoFrame bytes.
func ReadInfoFrame(r io.Reader) ([]byte, error) {
	n, err := ReadLen(r)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > MaxInfoFrame {
		return nil, fmt.Errorf("invalid length: %d", n)
	}
	buf, err := ReadExact(r, n)
	if err != nil {
		return nil, fmt.Errorf("error reading %d bytes: %w", n, err)
	}
	return buf, nil
}

// AppendFrame appends b with its length prefix to dst.
func AppendFrame(dst, b []byte) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(b)))
	return append(dst, b...)
}

// WriteFrame writes b with its length prefix in one write.
func WriteFrame(w io.Writer, b []byte) error {
	_, err := w.Write(AppendFrame(make([]byte, 0, 4+len(b)), b))
	return err
}

// ReadFlag reads a flag.
func ReadFlag(r io.Reader) (string, error) {
	var buf [flagLen]byte
	_, err := io.ReadFull(r, buf[:])
	return string(buf[:]), err
}

// AppendWork appends the request for work flag on a connection of a client
// with caps and node UUID uuid.
func AppendWork(dst []byte, caps Capabilities, flag, uuid string) ([]byte, error) {
	dst = append(dst, flag...)
	if caps.RandomPadding {
		if caps.NodeID {
			dst = AppendFrame(dst, []byte(uuid))
		}
		pad, err := randomPadding()
		if err != nil {
			return nil, err
		}
		dst = AppendFrame(dst, pad)
	}
	return dst, nil
}

// WriteWork writes the request for work flag in one write.
func WriteWork(w io.Writer, caps Capabilities, flag, uuid string) error {
	b, err := AppendWork(nil, caps, flag, uuid)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadWork reads a request for work; uuid is empty if caps carry none.
func ReadWork(r io.Reader, caps Capabilities) (flag, uuid string, err error) {
	if flag, err = ReadFlag(r); err != nil {
		return "", "", err
	}
	if caps.RandomPadding {
		if caps.NodeID {
			b, err := ReadFrame(r)
			if err != nil {
				return flag, "", fmt.Errorf("read uuid: %w", err)
			}
			uuid = string(b)
		}
		if _, err := ReadFrame(r); err != nil {
			return flag, uuid, fmt.Errorf("read padding: %w", err)
		}
	}
	return flag, uuid, nil
}

// FallbackNodeID names a node without a UUID of its own after its address,
// given as host:port.
func FallbackNodeID(caps Capabilities, addr string) string {
	if !caps.NodeIDHasPort {
		addr = hostOf(addr)
	}
	return NameUUID(addr)
}

// hostOf returns the host of an address, or the address when it has no port
// that can be told apart.
func hostOf(host string) string {
	if len(host) == 0 {
		return host
	}
	if host[0] == '[' {
		for i := range len(host) {
			if host[i] == ']' {
				return host[1:i]
			}
		}
		return ""
	}
	first, last := -1, -1
	for i := range len(host) {
		if host[i] == ':' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if last != -1 && last == first {
		return host[:last]
	}
	return host
}
