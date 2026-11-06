package ui

import (
	"strings"
	"testing"
)

func TestRenderManagementShellIncludesBootstrapAndCustomHead(t *testing.T) {
	html, err := RenderManagementShell(map[string]interface{}{
		"routes": map[string]interface{}{
			"api_base": "/api/v1",
		},
	}, ManagementShellMetadata{
		HeadCustomCode: `<meta name="shell-probe" content="present">`,
	})
	if err != nil {
		t.Fatalf("RenderManagementShell() error = %v", err)
	}
	if !strings.Contains(html, `id="nps-bootstrap"`) {
		t.Fatalf("RenderManagementShell() should include bootstrap script, got %q", html)
	}
	if !strings.Contains(html, `window.__NPS_BOOTSTRAP__`) {
		t.Fatalf("RenderManagementShell() should expose bootstrap payload, got %q", html)
	}
	if !strings.Contains(html, `shell-probe`) {
		t.Fatalf("RenderManagementShell() should include custom head code, got %q", html)
	}
}
