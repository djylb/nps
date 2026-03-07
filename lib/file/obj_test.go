package file

import (
	"sync"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
)

func TestFlowSubDoesNotGoNegative(t *testing.T) {
	flow := &Flow{InletFlow: 5, ExportFlow: 3}

	flow.Sub(10, 8)

	if flow.InletFlow != 0 {
		t.Fatalf("expected inlet flow to clamp at 0, got %d", flow.InletFlow)
	}
	if flow.ExportFlow != 0 {
		t.Fatalf("expected export flow to clamp at 0, got %d", flow.ExportFlow)
	}
}

func TestSortClientByKey(t *testing.T) {
	clients := &sync.Map{}
	clients.Store("a", &Client{Id: 1, Flow: &Flow{ExportFlow: 10, InletFlow: 100}})
	clients.Store("b", &Client{Id: 2, Flow: &Flow{ExportFlow: 30, InletFlow: 50}})
	clients.Store("c", &Client{Id: 3, Flow: &Flow{ExportFlow: 20, InletFlow: 80}})

	desc := sortClientByKey(clients, "ExportFlow", "desc")
	if len(desc) != 3 || desc[0] != 1 || desc[1] != 3 || desc[2] != 2 {
		t.Fatalf("unexpected desc sort result: %v", desc)
	}

	asc := sortClientByKey(clients, "InletFlow", "asc")
	if len(asc) != 3 || asc[0] != 1 || asc[1] != 3 || asc[2] != 2 {
		t.Fatalf("unexpected asc sort result: %v", asc)
	}
}

func TestEnsureWebPasswordExtractsAndRepairsTotpSecret(t *testing.T) {
	validSecret := "JBSWY3DPEHPK3PXP"
	c := &Client{
		WebPassword: "plainpass" + common.TOTP_SEQ + validSecret,
	}

	c.EnsureWebPassword()

	if c.WebPassword != "plainpass" {
		t.Fatalf("expected password suffix to be removed, got %q", c.WebPassword)
	}
	if c.WebTotpSecret != validSecret {
		t.Fatalf("expected extracted secret %q, got %q", validSecret, c.WebTotpSecret)
	}

	c.WebTotpSecret = "invalid"
	c.EnsureWebPassword()
	if c.WebTotpSecret == "invalid" {
		t.Fatalf("expected invalid secret to be regenerated")
	}
	if !crypt.IsValidTOTPSecret(c.WebTotpSecret) {
		t.Fatalf("expected regenerated secret to be valid, got %q", c.WebTotpSecret)
	}
}
