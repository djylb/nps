package crypt

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

const TotpLen = 6

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToUpper(b32.EncodeToString(buf)), nil
}

func ValidateTOTPCode(secret, code string) (bool, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false, err
	}
	pass, err := strconv.Atoi(code)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Unix() / 30
	for i := -1; i <= 1; i++ {
		counter := now + int64(i)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(counter))
		mac := hmac.New(sha1.New, key)
		mac.Write(buf)
		hash := mac.Sum(nil)
		offset := hash[len(hash)-1] & 0x0F
		val := (int(hash[offset])&0x7F)<<24 |
			(int(hash[offset+1])&0xFF)<<16 |
			(int(hash[offset+2])&0xFF)<<8 |
			(int(hash[offset+3]) & 0xFF)
		val %= 1000000
		if val == pass {
			return true, nil
		}
	}
	return false, nil
}

func GetTOTPCode(secret string) (string, int64, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", 0, err
	}
	now := time.Now().UTC().Unix()
	counter := now / 30
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0F
	val := (int(hash[offset])&0x7F)<<24 |
		(int(hash[offset+1])&0xFF)<<16 |
		(int(hash[offset+2])&0xFF)<<8 |
		(int(hash[offset+3]) & 0xFF)
	val %= 1000000
	code := fmt.Sprintf("%06d", val)
	remaining := 30 - (now % 30)
	return code, remaining, nil
}

func IsValidTOTPSecret(secret string) bool {
	if _, err := b32.DecodeString(secret); err != nil {
		return false
	}
	if _, _, err := GetTOTPCode(secret); err != nil {
		return false
	}
	return true
}

func BuildTotpUri(issuer, accountName, secret string) string {
	if issuer == "" {
		encodedLabel := url.QueryEscape(accountName)
		return fmt.Sprintf(
			"otpauth://totp/%s?secret=%s",
			encodedLabel,
			secret,
		)
	}
	label := fmt.Sprintf("%s:%s", issuer, accountName)
	encodedLabel := url.QueryEscape(label)
	encodedIssuer := url.QueryEscape(issuer)
	return fmt.Sprintf(
		"otpauth://totp/%s?secret=%s&issuer=%s",
		encodedLabel,
		secret,
		encodedIssuer,
	)
}

func PrintTOTPSecret() {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		fmt.Printf("Failed to generate 2FA secret: %v\n", err)
		return
	}
	fmt.Printf("Your new 2FA secret is: %s\nPlease add this secret to your nps.conf configuration file.\n", secret)
	totpUrl := BuildTotpUri("", "NPS", secret)
	qr, err := qrcode.New(totpUrl, qrcode.Medium)
	if err != nil {
		panic(err)
	}
	ascii := qr.ToString(false)
	fmt.Println(ascii)
}

func PrintTOTPCode(secret string) {
	code, rem, err := GetTOTPCode(secret)
	if err != nil {
		fmt.Printf("Failed to generate 2FA code: %v\n", err)
		return
	}
	ok, err := ValidateTOTPCode(secret, code)
	if err != nil {
		fmt.Printf("Failed to validate 2FA code: %v\n", err)
	}
	if ok {
		fmt.Printf("Your current 2FA code is: %s\n", code)
		fmt.Printf("It will expire in %d seconds.\n", rem)
		return
	}
	fmt.Printf("Failed to validate 2FA code.\n")
}
