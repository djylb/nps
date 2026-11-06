package api

import "github.com/djylb/nps/web/ui"

type PageModelResponse struct {
	Controller string                 `json:"controller"`
	Action     string                 `json:"action"`
	TplName    string                 `json:"tpl_name"`
	Layout     string                 `json:"layout,omitempty"`
	Data       map[string]interface{} `json:"data"`
	Page       PageEntry              `json:"page"`
}

func newPageModelResponse(baseURL string, spec PageSpec, page *ui.Page) *PageModelResponse {
	model := ui.PageModel(page)
	if model == nil {
		return nil
	}
	return &PageModelResponse{
		Controller: model.Controller,
		Action:     model.Action,
		TplName:    model.TplName,
		Layout:     model.Layout,
		Data:       model.Data,
		Page:       pageEntryFromSpec(baseURL, spec),
	}
}
