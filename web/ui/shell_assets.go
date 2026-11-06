package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djylb/nps/lib/common"
)

type ManagementShellAssets struct {
	Ready   bool     `json:"ready"`
	Styles  []string `json:"styles,omitempty"`
	Scripts []string `json:"scripts,omitempty"`
}

func (a ManagementShellAssets) Clone() ManagementShellAssets {
	return ManagementShellAssets{
		Ready:   a.Ready,
		Styles:  append([]string(nil), a.Styles...),
		Scripts: append([]string(nil), a.Scripts...),
	}
}

func DefaultManagementShellAssets(baseURL string) ManagementShellAssets {
	return ResolveManagementShellAssets(filepath.Join(common.GetRunPath(), "web", "static", "app"), joinShellAssetBase(baseURL, "/static/app"))
}

func ResolveManagementShellAssets(staticRoot, staticBase string) ManagementShellAssets {
	if assets, ok := resolveViteManagementShellAssets(staticRoot, staticBase); ok {
		return assets
	}
	if assets, ok := resolveAssetManifestShellAssets(staticRoot, staticBase); ok {
		return assets
	}
	return ManagementShellAssets{}
}

type viteManifestEntry struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	IsEntry bool     `json:"isEntry"`
}

func resolveViteManagementShellAssets(staticRoot, staticBase string) (ManagementShellAssets, bool) {
	data, err := os.ReadFile(filepath.Join(staticRoot, "manifest.json"))
	if err != nil {
		return ManagementShellAssets{}, false
	}
	var manifest map[string]viteManifestEntry
	if err := json.Unmarshal(data, &manifest); err != nil || len(manifest) == 0 {
		return ManagementShellAssets{}, false
	}
	entry, ok := pickViteManifestEntry(manifest)
	if !ok {
		return ManagementShellAssets{}, false
	}
	assets := ManagementShellAssets{
		Styles:  make([]string, 0, len(entry.CSS)),
		Scripts: make([]string, 0, 1),
	}
	appendShellAsset(&assets.Scripts, staticBase, entry.File, ".js", ".mjs", ".cjs")
	appendShellAsset(&assets.Styles, staticBase, entry.File, ".css")
	for _, css := range entry.CSS {
		appendShellAsset(&assets.Styles, staticBase, css, ".css")
	}
	assets.Ready = len(assets.Styles) > 0 || len(assets.Scripts) > 0
	return assets, assets.Ready
}

func pickViteManifestEntry(manifest map[string]viteManifestEntry) (viteManifestEntry, bool) {
	if entry, ok := manifest["index.html"]; ok && (entry.IsEntry || entry.File != "") {
		return entry, true
	}
	keys := make([]string, 0, len(manifest))
	for key := range manifest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := manifest[key]
		if entry.IsEntry {
			return entry, true
		}
	}
	if len(keys) == 1 {
		return manifest[keys[0]], true
	}
	return viteManifestEntry{}, false
}

type assetManifest struct {
	Files       map[string]string `json:"files"`
	Entrypoints []string          `json:"entrypoints"`
}

func resolveAssetManifestShellAssets(staticRoot, staticBase string) (ManagementShellAssets, bool) {
	data, err := os.ReadFile(filepath.Join(staticRoot, "asset-manifest.json"))
	if err != nil {
		return ManagementShellAssets{}, false
	}
	var manifest assetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ManagementShellAssets{}, false
	}
	assets := ManagementShellAssets{}
	for _, entry := range manifest.Entrypoints {
		appendShellAsset(&assets.Scripts, staticBase, entry, ".js", ".mjs", ".cjs")
		appendShellAsset(&assets.Styles, staticBase, entry, ".css")
	}
	if len(assets.Scripts) == 0 && len(assets.Styles) == 0 {
		appendShellAsset(&assets.Scripts, staticBase, manifest.Files["main.js"], ".js", ".mjs", ".cjs")
		appendShellAsset(&assets.Styles, staticBase, manifest.Files["main.css"], ".css")
	}
	assets.Ready = len(assets.Styles) > 0 || len(assets.Scripts) > 0
	return assets, assets.Ready
}

func appendShellAsset(target *[]string, staticBase, raw string, extensions ...string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !matchesShellAssetExtension(raw, extensions...) {
		return
	}
	value := normalizeShellAssetPath(staticBase, raw)
	for _, existing := range *target {
		if existing == value {
			return
		}
	}
	*target = append(*target, value)
}

func matchesShellAssetExtension(value string, extensions ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, extension := range extensions {
		if strings.HasSuffix(value, extension) {
			return true
		}
	}
	return false
}

func normalizeShellAssetPath(staticBase, value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"), strings.HasPrefix(value, "//"):
		return value
	case strings.HasPrefix(value, "/"):
		return value
	default:
		return strings.TrimRight(staticBase, "/") + "/" + strings.TrimLeft(value, "/")
	}
}

func joinShellAssetBase(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return suffix
	}
	return base + suffix
}
