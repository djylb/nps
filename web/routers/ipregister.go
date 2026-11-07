package routers

import (
	"net/http"
	"path/filepath"

	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/web/framework"
	"github.com/djylb/nps/web/ui"
	"github.com/gin-gonic/gin"
)

// InitIPRegister returns the handler of the registration page on its own
// listener and behind bridge://ipreg: the page at / and /ip/register, its
// static files and captcha, behind the global access lists.
func InitIPRegister() http.Handler {
	state := NewState(nil)
	ui.SetRoot(filepath.Join(runenv.GetRunPath(), "web", "views"))
	engine := gin.New()
	// The visitor address comes from webapi.VisitorIP; forwarding headers
	// are not trusted from any peer.
	_ = engine.SetTrustedProxies(nil)
	engine.Use(gin.Recovery())
	engine.Use(globalFirewallMiddleware(state))
	engine.Static("/static", filepath.Join(runenv.GetRunPath(), "web", "static"))
	engine.GET("/", handle(state.App.StandaloneIPRegisterPage))
	engine.GET("/ip/register", handle(state.App.StandaloneIPRegisterPage))
	engine.POST("/ip/register", handle(state.App.IPRegister))
	engine.GET("/captcha/new", framework.CaptchaNewHandler(""))
	engine.GET("/captcha/:id", framework.CaptchaImageHandler())
	return engine
}
