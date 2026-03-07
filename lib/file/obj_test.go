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

func TestTunnelCompileDestACLNormalizesAndHandlesEmptyRules(t *testing.T) {
	t.Run("invalid mode falls back to off", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: 99, DestAclRules: "Allow all"}
		tn.CompileDestACL()

		if tn.DestAclMode != AclOff {
			t.Fatalf("expected mode to fallback to off, got %d", tn.DestAclMode)
		}
		if tn.DestAclSet != nil {
			t.Fatalf("expected acl set to be nil in off mode")
		}
	})

	t.Run("whitelist with empty rules denies all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "  \r\n\t "}
		tn.CompileDestACL()

		if tn.DestAclRules != "" {
			t.Fatalf("expected normalized empty rules, got %q", tn.DestAclRules)
		}
		if tn.DestAclSet == nil {
			t.Fatalf("expected acl set to be created for empty whitelist")
		}
	})

	t.Run("blacklist with empty rules allows all", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist, DestAclRules: ""}
		tn.CompileDestACL()

		if tn.DestAclSet != nil {
			t.Fatalf("expected acl set to stay nil for empty blacklist")
		}
	})
}

func TestTunnelAllowsDestination(t *testing.T) {
	t.Run("nil tunnel allows all", func(t *testing.T) {
		var tn *Tunnel
		if !tn.AllowsDestination("example.com:443") {
			t.Fatalf("expected nil tunnel to allow destination")
		}
	})

	t.Run("whitelist allows matched and blocks unmatched", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "example.com"}
		tn.CompileDestACL()

		if !tn.AllowsDestination("example.com:443") {
			t.Fatalf("expected matched destination to be allowed in whitelist mode")
		}
		if tn.AllowsDestination("blocked.com:443") {
			t.Fatalf("expected unmatched destination to be denied in whitelist mode")
		}
	})

	t.Run("blacklist denies matched and allows unmatched", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclBlacklist, DestAclRules: "blocked.com"}
		tn.CompileDestACL()

		if tn.AllowsDestination("blocked.com:80") {
			t.Fatalf("expected matched destination to be denied in blacklist mode")
		}
		if !tn.AllowsDestination("safe.com:80") {
			t.Fatalf("expected unmatched destination to be allowed in blacklist mode")
		}
	})

	t.Run("on demand compile path works", func(t *testing.T) {
		tn := &Tunnel{DestAclMode: AclWhitelist, DestAclRules: "allow.me", DestAclSet: nil}

		if !tn.AllowsDestination("allow.me:8080") {
			t.Fatalf("expected destination to be allowed after on-demand compile")
		}
		if tn.DestAclSet == nil {
			t.Fatalf("expected on-demand compile to populate acl set")
		}
	})
}
