package wire

import (
	"crypto/sha256"

	"github.com/zmuxio/zmux-go/seal"
)

const (
	labelVKey = "nps vkey"
	domain    = "nps core"
)

// HandleLen is the length of a Handle; HelloHeadLen covers Profile, Flags and
// Handle at the start of an Establish Hello (§13).
const (
	HandleLen    = 16
	HelloHeadLen = 18
	MaxPeek      = 291 // Join Hello head (Listener.KnownJoin), §13
)

// Derive returns the Handle and PSK of a vkey of any shape (§3.1).
func Derive(vkey string) (handle, psk []byte, err error) {
	h := sha256.New()
	h.Write([]byte(labelVKey))
	h.Write([]byte(vkey))
	return seal.DeriveHandlePSK(h.Sum(nil), []byte(domain), nil)
}
