package crypt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SecretIndex names the secret tunnel of password in an attach request, so
// that nps finds the tunnel without a search; the password itself never
// travels. Like any hash of a password it is a verifier an observer could
// test guesses against, so it travels only inside the encrypted bridge
// connection.
func SecretIndex(password string) string {
	h := sha256.Sum256([]byte("nps secret index\x00" + password))
	return hex.EncodeToString(h[:])
}

// SecretProof is a visitor's proof that it knows password: an HMAC over the
// nonce nps sent for this attach and the visitor's node (npc uuid), so that
// a captured proof is good for no other attach.
func SecretProof(password string, nonce []byte, node string) []byte {
	key := sha256.Sum256([]byte("nps secret key\x00" + password))
	m := hmac.New(sha256.New, key[:])
	m.Write([]byte("nps secret attach\x00"))
	m.Write(nonce)
	m.Write([]byte{0})
	m.Write([]byte(node))
	return m.Sum(nil)
}
