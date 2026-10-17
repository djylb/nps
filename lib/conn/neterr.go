package conn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
)

func IsConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := normalizeNetErrorText(err)
	return strings.Contains(msg, "connectionresetbypeer") ||
		strings.Contains(msg, "forciblyclosedbytheremotehost")
}

func IsConnAborted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	msg := normalizeNetErrorText(err)
	return strings.Contains(msg, "connectionaborted") ||
		strings.Contains(msg, "softwarecausedconnectionabort")
}

func IsBrokenPipe(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := normalizeNetErrorText(err)
	return strings.Contains(msg, "brokenpipe")
}

func NetErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case IsConnReset(err):
		return "rst"
	case IsConnAborted(err):
		return "aborted"
	case IsBrokenPipe(err):
		return "broken_pipe"
	case IsTimeout(err):
		return "timeout"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, net.ErrClosed):
		return "closed"
	default:
		return "other"
	}
}

func DescribeNetError(err error, c net.Conn) string {
	if err == nil {
		return "kind=none"
	}

	parts := []string{
		fmt.Sprintf("kind=%s", NetErrorKind(err)),
		fmt.Sprintf("err=%q", err.Error()),
	}

	if c != nil {
		if local := c.LocalAddr(); local != nil {
			parts = append(parts, fmt.Sprintf("local=%s", local.String()))
		}
		if remote := c.RemoteAddr(); remote != nil {
			parts = append(parts, fmt.Sprintf("remote=%s", remote.String()))
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		parts = append(parts,
			fmt.Sprintf("timeout=%t", netErr.Timeout()),
			fmt.Sprintf("temporary=%t", netErr.Temporary()),
		)
	} else {
		parts = append(parts, fmt.Sprintf("timeout=%t", IsTimeout(err)))
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op != "" {
			parts = append(parts, fmt.Sprintf("op=%s", opErr.Op))
		}
		if opErr.Net != "" {
			parts = append(parts, fmt.Sprintf("net=%s", opErr.Net))
		}
		if opErr.Source != nil {
			parts = append(parts, fmt.Sprintf("source=%s", opErr.Source.String()))
		}
		if opErr.Addr != nil {
			parts = append(parts, fmt.Sprintf("addr=%s", opErr.Addr.String()))
		}
	}

	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		parts = append(parts, fmt.Sprintf("syscall=%s", sysErr.Syscall))
	}

	if errno, ok := extractErrno(err); ok {
		parts = append(parts,
			fmt.Sprintf("errno=%d", errno),
			fmt.Sprintf("errno_name=%s", errnoName(errno)),
		)
	}

	return strings.Join(parts, " ")
}

func normalizeNetErrorText(err error) string {
	s := strings.ToLower(err.Error())
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

func extractErrno(err error) (syscall.Errno, bool) {
	if err == nil {
		return 0, false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno, true
	}
	return 0, false
}

func errnoName(errno syscall.Errno) string {
	switch {
	case errors.Is(errno, syscall.ECONNRESET):
		return "ECONNRESET"
	case errors.Is(errno, syscall.ECONNABORTED):
		return "ECONNABORTED"
	case errors.Is(errno, syscall.EPIPE):
		return "EPIPE"
	default:
		return errno.Error()
	}
}
