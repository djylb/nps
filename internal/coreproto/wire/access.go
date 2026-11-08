package wire

import (
	"crypto/hmac"
	"crypto/sha256"
)

// The access field of a LINK is id ‖ proof, never the password (§9.1).
const (
	AccessIDLen    = 16
	AccessProofLen = 32
	// AccessExportLabel is the exporter label of the session key a proof is
	// bound to; a proof exports AccessExportLen bytes under it, with no
	// context. A split bootstrap gives twice that, the same on both ends.
	AccessExportLabel = "nps access"
	AccessExportLen   = 32
)

// AccessID is what the server finds a password by.
func AccessID(password []byte) []byte {
	h := sha256.New()
	h.Write([]byte("nps access"))
	h.Write(password)
	return h.Sum(nil)[:AccessIDLen]
}

// AccessProof binds password to the session whose exporter gave ekm.
func AccessProof(password, ekm []byte) []byte {
	m := hmac.New(sha256.New, password)
	m.Write(ekm)
	return m.Sum(nil)
}

// AppendAccess is the value of the access field.
func AppendAccess(password, ekm []byte) []byte {
	return append(AccessID(password), AccessProof(password, ekm)...)
}

// SplitAccess takes an access field apart; false when it has any other
// length, which is NPS_MALFORMED.
func SplitAccess(access []byte) (id, proof []byte, ok bool) {
	if len(access) != AccessIDLen+AccessProofLen {
		return nil, nil, false
	}
	return access[:AccessIDLen], access[AccessIDLen:], true
}
