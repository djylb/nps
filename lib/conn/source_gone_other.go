//go:build !unix

package conn

import "syscall"

// peekSocketClosed has no way to look here: a wait ends at its timeout.
func peekSocketClosed(syscall.RawConn) bool { return false }
