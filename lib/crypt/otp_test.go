package crypt

import (
	"regexp"
	"testing"
)

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if len(secret) != 32 {
		t.Fatalf("GenerateTOTPSecret() secret length = %d, want 32", len(secret))
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

	if !VerifyTOTP(secret, code) {
		t.Fatal("VerifyTOTP() = false, want true for generated code")
	}
	if VerifyTOTP(secret, code) {
		t.Fatal("VerifyTOTP() accepted a code twice")
	}
}

func TestVerifyTOTPInvalidInput(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	if VerifyTOTP("not-base32", "123456") {
		t.Fatal("VerifyTOTP(invalid secret) = true")
	}
	if VerifyTOTP(secret, "not-number") {
		t.Fatal("VerifyTOTP(invalid code) = true")
	}
}

func TestLegacyTOTPSecret(t *testing.T) {
	// An 80-bit secret, as earlier versions generated.
	secret := "JBSWY3DPEHPK3PXP"
	if !IsValidTOTPSecret(secret) {
		t.Fatalf("IsValidTOTPSecret(%q) = false", secret)
	}
	code, _, err := GetTOTPCode(secret)
	if err != nil || !VerifyTOTP(secret, code) {
		t.Fatalf("VerifyTOTP(%q) rejected the current code: %v", secret, err)
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
	withIssuer, err := BuildTotpUri("nps team", "alice@example.com", secret)
	wantWithIssuer := "otpauth://totp/nps%20team:alice@example.com?secret=" + secret + "&issuer=nps%20team"
	if err != nil || withIssuer != wantWithIssuer {
		t.Fatalf("BuildTotpUri(with issuer) = %q, want %q", withIssuer, wantWithIssuer)
	}

	withoutIssuer, err := BuildTotpUri("", "alice@example.com", secret)
	wantWithoutIssuer := "otpauth://totp/alice@example.com?secret=" + secret
	if err != nil || withoutIssuer != wantWithoutIssuer {
		t.Fatalf("BuildTotpUri(without issuer) = %q, want %q", withoutIssuer, wantWithoutIssuer)
	}
}

func TestBuildTotpUriRejectsInvalidSecret(t *testing.T) {
	if _, err := BuildTotpUri("", "alice", "%%%%"); err == nil {
		t.Fatal("BuildTotpUri(invalid secret) succeeded")
	}
}
