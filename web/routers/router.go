package routers

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/djylb/nps/lib/common"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	"github.com/djylb/nps/web/ui"
	"github.com/gin-gonic/gin"
)

func Init() http.Handler {
	state := NewState(nil)
	cfg := state.CurrentConfig()

	ui.SetRoot(filepath.Join(common.GetRunPath(), "web", "views"))
	framework.SetSessionConfigProvider(state.CurrentConfig)
	if err := framework.InitSessionStore(cfg); err != nil {
		panic(err)
	}

	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.HandleMethodNotAllowed = false
	engine.Use(gin.Recovery())
	engine.NoRoute(unknownRouteHandler(cfg.Web.CloseOnNotFound))
	engine.NoMethod(unknownRouteHandler(cfg.Web.CloseOnNotFound))

	engine.Static(joinBase(cfg.Web.BaseURL, "/static"), filepath.Join(common.GetRunPath(), "web", "static"))

	basePath := strings.TrimRight(strings.TrimSpace(cfg.Web.BaseURL), "/")
	group := engine.Group(basePath)
	group.Use(sessionMiddleware())
	group.Use(requestContextMiddleware(state))
	group.GET("/captcha/new", framework.CaptchaNewHandler(cfg.Web.BaseURL))
	group.GET("/captcha/:id", framework.CaptchaImageHandler())
	registerSessionRoutes(group, state)

	protected := group.Group("")
	protected.Use(protectedRouteMiddleware(state))
	registerProtectedRoutes(protected, state)
	if basePath != "" {
		protected.GET("", redirectToIndex(state))
	}
	protected.GET("/", redirectToIndex(state))

	return engine
}

func unknownRouteHandler(closeOnNotFound bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if closeOnNotFound && dropConnection(c) {
			c.Abort()
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}

func dropConnection(c *gin.Context) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	unwrapper, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		return false
	}
	hijacker, ok := unwrapper.Unwrap().(http.Hijacker)
	if !ok {
		return false
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func registerSessionRoutes(group *gin.RouterGroup, state *State) {
	sessionActions := webapi.SessionActionCatalog(state.App)
	registerSessionSupportRoutes(group, state)
	registerDirectPageCatalog(group, state, "login", webapi.SessionPageSpecs())
	registerSessionControllerRoutes(group, state, "login", sessionActions)
	registerSessionControllerRoutes(group, state, "auth", sessionActions)
	group.GET("/login/out", handle(state.App.LogoutRedirect))
	registerSessionPageRoutes(group, state)
}

func registerSessionSupportRoutes(group *gin.RouterGroup, state *State) {
	cfg := state.CurrentConfig()
	api := group.Group("/api/v1")
	api.GET("/captcha/new", framework.CaptchaNewHandler(joinBase(cfg.Web.BaseURL, "/api/v1")))
	api.GET("/captcha/:id", framework.CaptchaImageHandler())
}

func registerProtectedRoutes(group *gin.RouterGroup, state *State) {
	registerIndexRoutes(group, state)
	registerClientRoutes(group, state)
	registerGlobalRoutes(group, state)
	registerProtectedPageRoutes(group, state)
	registerManagementShellRoutes(group, state)
}

func registerSessionPageRoutes(group *gin.RouterGroup, state *State) {
	registerPageCatalog(group, "/auth", state, webapi.SessionPageSpecs())
	registerPageCatalog(group, "/api", state, webapi.SessionPageSpecs())
}

func registerIndexRoutes(group *gin.RouterGroup, state *State) {
	registerProtectedControllerRoutes(group, state, "index", webapi.ProtectedActionCatalog(state.App))
}

func registerClientRoutes(group *gin.RouterGroup, state *State) {
	registerProtectedControllerRoutes(group, state, "client", webapi.ProtectedActionCatalog(state.App))
}

func registerGlobalRoutes(group *gin.RouterGroup, state *State) {
	registerProtectedControllerRoutes(group, state, "global", webapi.ProtectedActionCatalog(state.App))
}

func registerProtectedPageRoutes(group *gin.RouterGroup, state *State) {
	apiApp := state.App
	registerPageCatalog(group, "/auth", state, webapi.ProtectedPageSpecs())
	registerPageCatalog(group, "/api", state, webapi.ProtectedPageSpecs())

	group.GET("/auth/page", pageModel(apiApp, "index", "index"))
	group.GET("/auth/render", renderPage(apiApp, "index", "index"))
	group.GET("/api/page", pageModel(apiApp, "index", "index"))
	group.GET("/api/render", renderPage(apiApp, "index", "index"))
}

func registerManagementShellRoutes(group *gin.RouterGroup, state *State) {
	apiApp := state.App
	group.GET("/app", handle(apiApp.ManagementShell))
	group.GET("/app/*path", handle(apiApp.ManagementShell))
}

func redirectToIndex(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Redirect(http.StatusFound, joinBase(state.BaseURL(), "/index/index"))
	}
}

func renderPage(apiApp *webapi.App, controller, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiApp.RenderPage(newAPIContext(c), controller, action)
	}
}

func renderControllerPage(apiApp *webapi.App, controller, defaultAction string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiApp.RenderControllerPage(newAPIContext(c), controller, defaultAction)
	}
}

func pageModel(apiApp *webapi.App, controller, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiApp.PageModel(newAPIContext(c), controller, action)
	}
}

func pageModelController(apiApp *webapi.App, controller, defaultAction string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiApp.PageModelController(newAPIContext(c), controller, defaultAction)
	}
}

func handle(fn webapi.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		fn(newAPIContext(c))
	}
}

func registerPageCatalog(group *gin.RouterGroup, prefix string, state *State, specs []webapi.PageSpec) {
	for _, spec := range state.AvailablePageSpecs(specs) {
		registerPageSpec(group, prefix, state, spec)
	}
}

func registerDirectPageCatalog(group *gin.RouterGroup, state *State, controller string, specs []webapi.PageSpec) {
	apiApp := state.App
	for _, spec := range state.AvailablePageSpecs(specs) {
		if spec.Controller != controller {
			continue
		}
		handlers := appendPageHandler(pageCatalogMiddlewares(state, spec), renderPage(apiApp, spec.Controller, spec.Action))
		group.GET("/"+spec.Controller+"/"+spec.Action, handlers...)
	}
}

func registerPageSpec(group *gin.RouterGroup, prefix string, state *State, spec webapi.PageSpec) {
	apiApp := state.App
	modelHandlers := appendPageHandler(pageCatalogMiddlewares(state, spec), pageModel(apiApp, spec.Controller, spec.Action))
	renderHandlers := appendPageHandler(pageCatalogMiddlewares(state, spec), renderPage(apiApp, spec.Controller, spec.Action))

	group.GET(prefix+"/page/"+spec.Controller+"/"+spec.Action, modelHandlers...)
	group.GET(prefix+"/render/"+spec.Controller+"/"+spec.Action, renderHandlers...)

	if !spec.IncludeControllerRoot {
		return
	}

	rootModelHandlers := appendPageHandler(pageCatalogMiddlewares(state, spec), pageModelController(apiApp, spec.Controller, spec.Action))
	rootRenderHandlers := appendPageHandler(pageCatalogMiddlewares(state, spec), renderControllerPage(apiApp, spec.Controller, spec.Action))
	group.GET(prefix+"/page/"+spec.Controller, rootModelHandlers...)
	group.GET(prefix+"/render/"+spec.Controller, rootRenderHandlers...)
}

func pageCatalogMiddlewares(state *State, spec webapi.PageSpec) []gin.HandlerFunc {
	middlewares := make([]gin.HandlerFunc, 0, 2)
	if spec.Permission != "" {
		middlewares = append(middlewares, permissionMiddleware(state, spec.Permission))
	}
	switch spec.Ownership {
	case webapi.PageOwnershipClient:
		middlewares = append(middlewares, clientOwnershipMiddleware(state))
	case webapi.PageOwnershipTunnel:
		middlewares = append(middlewares, tunnelOwnershipMiddleware(state))
	case webapi.PageOwnershipHost:
		middlewares = append(middlewares, hostOwnershipMiddleware(state))
	}
	return middlewares
}

func appendPageHandler(middlewares []gin.HandlerFunc, handler gin.HandlerFunc) []gin.HandlerFunc {
	handlers := make([]gin.HandlerFunc, 0, len(middlewares)+1)
	handlers = append(handlers, middlewares...)
	handlers = append(handlers, handler)
	return handlers
}

func joinBase(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return suffix
	}
	return base + suffix
}
