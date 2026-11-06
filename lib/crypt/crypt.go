package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"math/big"
	"time"

	"github.com/djylb/nps/internal/tunnelwire"
	"github.com/google/uuid"
)

func AesEncrypt(origData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origData = PKCS5Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

// PKCS5Padding Completion when the length is insufficient
func PKCS5Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

// Md5 Generate 32-bit MD5 strings
func Md5(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Blake2b Generate 64-bit BLAKE2b-256 strings
func Blake2b(s string) string {
	return tunnelwire.KeyDigest(s)
}

func FNV1a64(parts ...string) string {
	h := fnv.New64a()
	for _, s := range parts {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil) // 8 bytes
	return hex.EncodeToString(sum)
}

func GetUUID() uuid.UUID {
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return u
}

// GetRandomString 生成指定长度的随机密钥，支持可选传入id
func GetRandomString(l int, id ...int) string {
	// 字符集
	str := "0123456789abcdefghijklmnopqrstuvwxyz"
	dictBytes := []byte(str)
	var result []byte

	// 如果传入id，则将id转换为字符集映射并倒序放在最前面
	if len(id) > 0 {
		// 将id转为字符集表示的字符串
		idMapped := ""
		for id[0] > 0 {
			idMapped = string(str[id[0]%len(str)]) + idMapped
			id[0] /= len(str)
		}

		// 如果倒序后的id长度超过指定长度l，则截断
		//if len(idMapped) > l {
		//	idMapped = idMapped[:l]
		//}

		// 将倒序后的id添加到结果中
		result = append(result, []byte(idMapped)...)
	}

	// 计算剩余需要生成的随机字符的长度
	remainingLength := l - len(result)
	if remainingLength > 0 {
		// 使用当前时间的UnixNano作为随机数种子
		//r := rand.New(rand.NewSource(time.Now().UnixNano()))
		// 生成剩余的随机字符
		for i := 0; i < remainingLength; i++ {
			//result = append(result, dictBytes[r.Intn(len(dictBytes))])
			nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(dictBytes))))
			if err != nil {
				// 如果安全随机生成失败，回退到时间戳伪随机
				idx := int(time.Now().UnixNano() % int64(len(dictBytes)))
				result = append(result, dictBytes[idx])
				continue
			}
			result = append(result, dictBytes[int(nBig.Int64())])
		}
	}

	// 返回最终结果字符串
	return string(result)
}
