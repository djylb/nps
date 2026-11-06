package api

import (
	"errors"
	"html/template"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

var errPageNotFound = errors.New("page not found")

func (a *App) RenderPage(c Context, controller, action string) {
	spec, page, err := a.buildPage(c, controller, action)
	if err != nil {
		respondEmpty(c, 404)
		return
	}
	if spec.Template == "" || page == nil || c.IsWritten() {
		return
	}
	renderPage(c, page)
}

func (a *App) RenderControllerPage(c Context, controller string, defaultAction string) {
	action := requestString(c, "action")
	if action == "" {
		action = defaultAction
	}
	a.RenderPage(c, controller, action)
}

func (a *App) PageModel(c Context, controller, action string) {
	spec, page, err := a.buildPage(c, controller, action)
	if err != nil {
		respondEmpty(c, 404)
		return
	}
	if spec.Template == "" || page == nil || c.IsWritten() {
		return
	}
	renderPageModel(c, a.currentConfig().Web.BaseURL, spec, page)
}

func (a *App) PageModelController(c Context, controller string, defaultAction string) {
	action := requestString(c, "action")
	if action == "" {
		action = defaultAction
	}
	a.PageModel(c, controller, action)
}

func (a *App) buildPage(c Context, controller, action string) (PageSpec, *ui.Page, error) {
	spec, ok := FindPageSpec(a.currentConfig(), controller, action)
	if !ok {
		return PageSpec{}, nil, errPageNotFound
	}
	if spec.Controller == "login" {
		page, err := a.buildLoginPage(c, spec)
		return spec, page, err
	}

	result, err := a.Services.Pages.Build(webservice.PageBuildInput{
		Config:     a.currentConfig(),
		Controller: spec.Controller,
		Action:     spec.Action,
		Host:       c.Host(),
		IsAdmin:    sessionBool(c, "isAdmin"),
		Username:   sessionString(c, "username"),
		Params:     pageBuildParams(c, spec),
	})
	if err != nil {
		return PageSpec{}, nil, errPageNotFound
	}
	return spec, newManagedSpecPage(spec, result.Data), nil
}

func (a *App) buildLoginPage(c Context, spec PageSpec) (*ui.Page, error) {
	cfg := a.currentConfig()
	info := a.system().Info()
	commonData := ui.LoginPageCommonData{
		WebBaseURL:     cfg.Web.BaseURL,
		HeadCustomCode: template.HTML(cfg.Web.HeadCustomCode),
		Version:        info.Version,
		Year:           info.Year,
		CaptchaOpen:    cfg.Feature.OpenCaptcha,
	}
	if cfg.Feature.OpenCaptcha {
		commonData.CaptchaHTML = framework.NewCaptchaHTML(cfg.Web.BaseURL)
	}
	data := commonData.Map()

	switch spec.Action {
	case "index":
		if c.SessionValue("auth") == true {
			c.Redirect(302, joinBase(cfg.Web.BaseURL, "/index/index"))
			return nil, nil
		}
		nonce := crypt.GetRandomString(16)
		setSession(c, "login_nonce", nonce)
		data["login_nonce"] = nonce
		data["pow_bits"] = cfg.Security.PoWBits
		data["totp_len"] = crypt.TotpLen
		data["pow_enable"] = cfg.Security.ForcePoW
		data["login_delay"] = a.loginPolicy().Settings().LoginDelayMillis()
		data["register_allow"] = cfg.Feature.AllowUserRegister
		data["public_key"], _ = crypt.GetRSAPublicKeyPEM()
		return newStandaloneSpecPage(spec, data), nil
	case "register":
		nonce := crypt.GetRandomString(16)
		setSession(c, "login_nonce", nonce)
		data["login_nonce"] = nonce
		data["public_key"], _ = crypt.GetRSAPublicKeyPEM()
		return newStandaloneSpecPage(spec, data), nil
	default:
		return nil, errPageNotFound
	}
}

func toPageData(data map[string]interface{}) map[string]interface{} {
	pageData := make(map[string]interface{}, len(data))
	for key, value := range data {
		pageData[key] = value
	}
	return pageData
}

func pageBuildParams(c Context, spec PageSpec) map[string]string {
	if len(spec.Params) == 0 {
		return nil
	}
	params := make(map[string]string, len(spec.Params))
	for _, param := range spec.Params {
		params[param.Name] = requestEscapedString(c, param.Name)
	}
	return params
}
