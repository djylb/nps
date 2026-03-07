package crypt

import (
	"net/url"
	"regexp"
	"testing"
)

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if len(secret) != 16 {
		t.Fatalf("GenerateTOTPSecret() secret length = %d, want 16", len(secret))
	}

	if matched := regexp.MustCompile(`^[A-Z2-7]+$`).MatchString(secret); !matched {
		t.Fatalf("GenerateTOTPSecret() secret %q contains invalid base32 characters", secret)
	}
}

func TestTOTPCodeRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	code, remaining, err := GetTOTPCode(secret)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}

	if len(code) != TotpLen {
		t.Fatalf("GetTOTPCode() code length = %d, want %d", len(code), TotpLen)
	}
	if remaining < 1 || remaining > 30 {
		t.Fatalf("GetTOTPCode() remaining = %d, want in [1, 30]", remaining)
	}

	ok, err := ValidateTOTPCode(secret, code)
	if err != nil {
		t.Fatalf("ValidateTOTPCode() error = %v", err)
	}
	if !ok {
		t.Fatal("ValidateTOTPCode() = false, want true for generated code")
	}
}

func TestValidateTOTPCode_InvalidInput(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if ok, err := ValidateTOTPCode("not-base32", "123456"); err == nil || ok {
		t.Fatalf("ValidateTOTPCode(invalid secret) = (%v, %v), want (false, error)", ok, err)
	}

	if ok, err := ValidateTOTPCode(secret, "not-number"); err == nil || ok {
		t.Fatalf("ValidateTOTPCode(invalid code) = (%v, %v), want (false, error)", ok, err)
	}
}

func TestIsValidTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	if !IsValidTOTPSecret(secret) {
		t.Fatalf("IsValidTOTPSecret(%q) = false, want true", secret)
	}
	if IsValidTOTPSecret("%%%%") {
		t.Fatal("IsValidTOTPSecret(%%%%) = true, want false")
	}
}

func TestBuildTotpUri(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	withIssuer := BuildTotpUri("nps team", "alice@example.com", secret)
	wantWithIssuer := "otpauth://totp/" + url.QueryEscape("nps team:alice@example.com") + "?secret=" + secret + "&issuer=" + url.QueryEscape("nps team")
	if withIssuer != wantWithIssuer {
		t.Fatalf("BuildTotpUri(with issuer) = %q, want %q", withIssuer, wantWithIssuer)
	}

	withoutIssuer := BuildTotpUri("", "alice@example.com", secret)
	wantWithoutIssuer := "otpauth://totp/" + url.QueryEscape("alice@example.com") + "?secret=" + secret
	if withoutIssuer != wantWithoutIssuer {
		t.Fatalf("BuildTotpUri(without issuer) = %q, want %q", withoutIssuer, wantWithoutIssuer)
	}
}
