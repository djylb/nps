package crypt

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/djylb/guard/otp"
	"github.com/skip2/go-qrcode"
)

// TotpLen is the length of a TOTP code.
const TotpLen = otp.DefaultDigits

// totpKey returns the TOTP key of a base32 secret. Secrets of 80 bits,
// as earlier versions generated, are accepted.
func totpKey(secret string) (*otp.Key, error) {
	raw, err := otp.DecodeSecret(strings.TrimSpace(secret))
	if err != nil {
		return nil, err
	}
	key := &otp.Key{Secret: raw, SkewSteps: 1, AllowShortSecret: true}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	return key, nil
}

// GenerateTOTPSecret returns a new random secret in base32.
func GenerateTOTPSecret() (string, error) {
	raw, err := otp.GenerateSecret(otp.DefaultSecretSize)
	if err != nil {
		return "", err
	}
	return otp.EncodeSecret(raw), nil
}

// IsValidTOTPSecret reports whether secret is a usable base32 secret.
func IsValidTOTPSecret(secret string) bool {
	_, err := totpKey(secret)
	return err == nil
}

// totpSteps holds the next acceptable time step of each secret, so that a
// code is accepted once only.
var totpSteps struct {
	sync.Mutex
	next map[string]uint64
}

// VerifyTOTP reports whether code is a current code of secret that was
// not accepted before.
func VerifyTOTP(secret, code string) bool {
	key, err := totpKey(secret)
	if err != nil {
		return false
	}
	id := otp.EncodeSecret(key.Secret)
	totpSteps.Lock()
	defer totpSteps.Unlock()
	next, err := key.Verify(code, time.Now(), totpSteps.next[id])
	if err != nil {
		return false
	}
	if totpSteps.next == nil {
		totpSteps.next = make(map[string]uint64)
	}
	totpSteps.next[id] = next
	return true
}

// GetTOTPCode returns the current code of secret and the seconds it stays
// current.
func GetTOTPCode(secret string) (string, int64, error) {
	key, err := totpKey(secret)
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	code, err := key.Code(now)
	if err != nil {
		return "", 0, err
	}
	period := int64(otp.DefaultPeriod / time.Second)
	return code, period - now.Unix()%period, nil
}

// BuildTotpUri returns the otpauth URI of secret for authenticator apps.
func BuildTotpUri(issuer, accountName, secret string) (string, error) {
	key, err := totpKey(secret)
	if err != nil {
		return "", err
	}
	key.Issuer, key.Account = issuer, accountName
	return key.URI()
}

// PrintTOTPSecret prints a new secret and its QR code.
func PrintTOTPSecret() {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		fmt.Printf("Failed to generate 2FA secret: %v\n", err)
		return
	}
	fmt.Printf("Your new 2FA secret is: %s\nPlease add this secret to your nps.conf configuration file.\n", secret)
	totpUrl, err := BuildTotpUri("", "NPS", secret)
	if err != nil {
		fmt.Printf("Failed to build 2FA URI: %v\n", err)
		return
	}
	qr, err := qrcode.New(totpUrl, qrcode.Medium)
	if err != nil {
		fmt.Printf("Failed to build 2FA QR code: %v\n", err)
		return
	}
	fmt.Println(qr.ToString(false))
}

// PrintTOTPCode prints the current code of secret.
func PrintTOTPCode(secret string) {
	code, rem, err := GetTOTPCode(secret)
	if err != nil {
		fmt.Printf("Failed to generate 2FA code: %v\n", err)
		return
	}
	fmt.Printf("Your current 2FA code is: %s\n", code)
	fmt.Printf("It will expire in %d seconds.\n", rem)
}
