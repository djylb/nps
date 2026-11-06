package tunnelwire

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// KeyDigest is the digest of a client key sent by TimestampAuth handshakes:
// 64 hexadecimal digits of its BLAKE2b-256.
func KeyDigest(key string) string {
	sum := blake2b256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// MD5Hex is 32 hexadecimal digits of the MD5 of s, as index 0 handshakes and
// secret passwords send it.
func MD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Seal encrypts data with key: AES-256-GCM under the SHA-256 of key, with the
// nonce in front. An empty key leaves data as it is.
func Seal(data []byte, key string) ([]byte, error) {
	if key == "" {
		return data, nil
	}
	gcm, err := keyGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("io.ReadFull: %w", err)
	}
	ct := gcm.Seal(nil, nonce, data, nil)
	return append(nonce, ct...), nil
}

// Open decrypts what Seal encrypted.
func Open(enc []byte, key string) ([]byte, error) {
	if key == "" {
		return enc, nil
	}
	gcm, err := keyGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(enc) < ns+gcm.Overhead() {
		return nil, fmt.Errorf("ciphertext too short: %d", len(enc))
	}
	pt, err := gcm.Open(nil, enc[:ns], enc[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return pt, nil
}

func keyGCM(key string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return gcm, nil
}

// handshakeMAC is the HMAC-SHA-256 of the big-endian timestamp and pieces.
func handshakeMAC(key string, ts int64, pieces ...[]byte) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(binary.BigEndian.AppendUint64(nil, uint64(ts)))
	for _, p := range pieces {
		mac.Write(p)
	}
	return mac.Sum(nil)
}

// randomPadding returns up to paddingMax random bytes.
func randomPadding() ([]byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(paddingMax+1))
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n.Int64())
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// uuidNamespace is the URL namespace of RFC 9562.
var uuidNamespace = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// NameUUID is the version 5 UUID of parts joined by slashes, in the URL
// namespace.
func NameUUID(parts ...string) string {
	h := sha1.New()
	h.Write(uuidNamespace[:])
	h.Write([]byte(strings.Join(parts, "/")))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	s := hex.EncodeToString(u[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
