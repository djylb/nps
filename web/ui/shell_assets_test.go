package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveManagementShellAssetsFromViteManifest(t *testing.T) {
	root := t.TempDir()
	manifest := `{"index.html":{"file":"assets/app-123.js","css":["assets/app-123.css"],"isEntry":true}}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}

	assets := ResolveManagementShellAssets(root, "/static/app")
	if !assets.Ready {
		t.Fatalf("ResolveManagementShellAssets() should detect assets, got %+v", assets)
	}
	if len(assets.Scripts) != 1 || assets.Scripts[0] != "/static/app/assets/app-123.js" {
		t.Fatalf("ResolveManagementShellAssets() scripts = %#v", assets.Scripts)
	}
	if len(assets.Styles) != 1 || assets.Styles[0] != "/static/app/assets/app-123.css" {
		t.Fatalf("ResolveManagementShellAssets() styles = %#v", assets.Styles)
	}
}

func TestRenderManagementShellWithAssetsInjectsExternalBundle(t *testing.T) {
	html, err := RenderManagementShellWithAssets(map[string]interface{}{
		"routes": map[string]interface{}{
			"api_base": "/api/v1",
		},
	}, ManagementShellMetadata{
		HeadCustomCode: `<meta name="shell-probe" content="present">`,
	}, ManagementShellAssets{
		Ready:   true,
		Styles:  []string{"/static/app/assets/app.css"},
		Scripts: []string{"/static/app/assets/app.js"},
	})
	if err != nil {
		t.Fatalf("RenderManagementShellWithAssets() error = %v", err)
	}
	if !strings.Contains(html, `/static/app/assets/app.css`) || !strings.Contains(html, `/static/app/assets/app.js`) {
		t.Fatalf("RenderManagementShellWithAssets() should include external assets, got %q", html)
	}
	if !strings.Contains(html, `window.__NPS_BOOTSTRAP__`) {
		t.Fatalf("RenderManagementShellWithAssets() should expose bootstrap payload, got %q", html)
	}
	if strings.Contains(html, "Available Management Pages") {
		t.Fatalf("RenderManagementShellWithAssets() should skip fallback shell markup when external assets are ready, got %q", html)
	}
}
