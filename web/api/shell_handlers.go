package api

import "github.com/djylb/nps/web/ui"

func (a *App) ManagementShell(c Context) {
	cfg := a.currentConfig()
	assets := a.managementShellAssets(cfg)
	metadata := a.managementShellMetadata(cfg)
	html, err := ui.RenderManagementShellWithAssets(a.bootstrapPayloadWithShellAssets(c, assets), metadata, assets)
	if err != nil {
		respondEmpty(c, 500)
		return
	}
	c.RespondData(200, "text/html; charset=utf-8", []byte(html))
}
