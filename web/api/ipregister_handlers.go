package api

import (
	"errors"
	"html/template"
	"strconv"
	"time"

	"github.com/djylb/nps/lib/ipreg"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

// ipRegisterSpec is the public page on which visitors register their
// address for a client with its registration key or verify key.
var ipRegisterSpec = PageSpec{Controller: "ip", Action: "register", Template: "login/ipregister.html"}

// IPRegisterResponse answers a registration.
type IPRegisterResponse struct {
	Status int    `json:"status"`
	Msg    string `json:"msg"`
	IP     string `json:"ip,omitempty"`
	Until  string `json:"until,omitempty"`
}

// IPRegisterPage renders the registration page of the web interface; a key
// query parameter fills in the key, so that a link can be shared.
func (a *App) IPRegisterPage(c Context) {
	a.renderIPRegisterPage(c, a.currentConfig().Web.BaseURL, true)
}

// StandaloneIPRegisterPage renders the registration page of its own
// listener, which serves it from the root.
func (a *App) StandaloneIPRegisterPage(c Context) {
	a.renderIPRegisterPage(c, "", false)
}

// renderIPRegisterPage renders the page with paths under base; login links
// back to the login page of the web interface.
func (a *App) renderIPRegisterPage(c Context, base string, login bool) {
	cfg := a.currentConfig()
	info := a.system().Info()
	data := ui.LoginPageCommonData{
		WebBaseURL:     base,
		HeadCustomCode: template.HTML(cfg.Web.HeadCustomCode),
		Version:        info.Version,
		Year:           info.Year,
		CaptchaOpen:    cfg.Feature.OpenCaptcha,
	}
	if cfg.Feature.OpenCaptcha {
		data.CaptchaHTML = framework.NewCaptchaHTML(base)
	}
	m := data.Map()
	m["key"] = requestString(c, "key")
	m["ip"] = requestVisitorIP(c, cfg)
	m["hour_options"] = ipRegisterHourOptions
	m["login_link"] = login
	renderPage(c, newStandaloneSpecPage(ipRegisterSpec, m))
}

// IPRegisterHourOption is a duration offered on the registration page.
type IPRegisterHourOption struct {
	Hours int
	Label string
}

// ipRegisterHourOptions are the durations offered on the registration page;
// the server cuts a choice above the key's limit down to it.
var ipRegisterHourOptions = []IPRegisterHourOption{
	{1, "1h"}, {2, "2h"}, {4, "4h"}, {8, "8h"}, {12, "12h"},
	{24, "1d"}, {72, "3d"}, {168, "7d"}, {720, "30d"},
}

// IPRegister registers the address of the visitor. Wrong keys count as
// failed logins, so that guessing them leads to a ban.
func (a *App) IPRegister(c Context) {
	cfg := a.currentConfig()
	policy := a.loginPolicy()
	ip := requestVisitorIP(c, cfg)
	if policy.IsIPBanned(ip) {
		c.RespondJSON(200, IPRegisterResponse{Msg: "too many attempts, try again later"})
		return
	}
	if cfg.Feature.OpenCaptcha && !framework.VerifyCaptcha(requestString(c, framework.CaptchaIDField), requestString(c, framework.CaptchaValueField)) {
		policy.RecordFailure(ip, true)
		c.RespondJSON(200, IPRegisterResponse{Msg: "the verification code is wrong"})
		return
	}
	id, until, err := a.Services.Clients.RegisterVisitorIP(requestString(c, "key"), ip, requestIntValue(c, "hours"))
	if err != nil {
		if errors.Is(err, webservice.ErrRegisterKeyInvalid) {
			policy.RecordFailure(ip, true)
		}
		c.RespondJSON(200, IPRegisterResponse{Msg: "invalid registration key"})
		return
	}
	owner := "client " + strconv.Itoa(id)
	if id == ipreg.AnyClient {
		owner = "every client"
	}
	logs.Info("Registered IP %s for %s until %s on the registration page", ip, owner, until.Format(time.DateTime))
	c.RespondJSON(200, IPRegisterResponse{Status: 1, Msg: "ip registered", IP: ip, Until: until.Format(time.DateTime)})
}
