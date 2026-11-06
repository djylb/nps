package routers

import (
	"net/http"
	"strings"

	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func registerSessionControllerRoutes(group *gin.RouterGroup, state *State, controller string, specs []webapi.ActionSpec) {
	filtered := filterActionSpecsByController(specs, controller)
	if len(filtered) == 0 {
		return
	}

	legacyPrefix := "/" + controller
	legacy := group.Group("/" + controller)
	registerActionRouteGroup(legacy, state, filtered, legacyPrefix, false)

	apiPrefix := "/api/v1/" + controller
	api := group.Group("/api/v1/" + controller)
	api.Use(apiInputMiddleware())
	registerActionRouteGroup(api, state, filtered, apiPrefix, true)
}

func registerProtectedControllerRoutes(group *gin.RouterGroup, state *State, controller string, specs []webapi.ActionSpec) {
	registerDirectPageCatalog(group, state, controller, webapi.ProtectedPageSpecs())
	filtered := filterActionSpecsByController(specs, controller)
	if len(filtered) == 0 {
		return
	}

	legacyPrefix := "/" + controller
	legacy := group.Group("/" + controller)
	registerActionRouteGroup(legacy, state, filtered, legacyPrefix, false)

	apiPrefix := "/api/v1/" + controller
	api := group.Group("/api/v1/" + controller)
	api.Use(apiInputMiddleware())
	registerActionRouteGroup(api, state, filtered, apiPrefix, true)
}

func filterActionSpecsByController(specs []webapi.ActionSpec, controller string) []webapi.ActionSpec {
	filtered := make([]webapi.ActionSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Controller == controller {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

func registerActionRouteGroup(group *gin.RouterGroup, state *State, specs []webapi.ActionSpec, routePrefix string, apiGroup bool) {
	for _, spec := range specs {
		handlers := appendActionHandler(actionRouteMiddlewares(state, spec), handle(spec.Handler))
		path := spec.LegacyPath
		if apiGroup {
			path = spec.APIPath
		}
		path = actionRelativePath(routePrefix, path)
		registerActionRoute(group, spec.Method, path, handlers)
	}
}

func actionRouteMiddlewares(state *State, spec webapi.ActionSpec) []gin.HandlerFunc {
	middlewares := make([]gin.HandlerFunc, 0, 3)
	if spec.Controller == "login" && (spec.Name == "verify" || spec.Name == "register") {
		middlewares = append(middlewares, loginBodyLimitMiddleware(state))
	}
	if permission := strings.TrimSpace(spec.Permission); permission != "" {
		middlewares = append(middlewares, permissionMiddleware(state, permission))
	}
	if spec.ClientScope {
		middlewares = append(middlewares, clientScopeMiddleware(state, spec.Ownership != webapi.ActionOwnershipNone))
	}
	switch spec.Ownership {
	case webapi.ActionOwnershipClient:
		middlewares = append(middlewares, clientOwnershipMiddleware(state))
	case webapi.ActionOwnershipTunnel:
		middlewares = append(middlewares, tunnelOwnershipMiddleware(state))
	case webapi.ActionOwnershipHost:
		middlewares = append(middlewares, hostOwnershipMiddleware(state))
	}
	return middlewares
}

func loginBodyLimitMiddleware(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Body == nil {
			c.Next()
			return
		}

		maxBody := int64(0)
		if state != nil {
			maxBody = state.LoginPolicy().Settings().MaxLoginBody
		}
		if maxBody <= 0 {
			c.Next()
			return
		}

		if c.Request.ContentLength > maxBody {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
		contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
		if c.Request.Method == http.MethodPost &&
			(strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
				strings.HasPrefix(contentType, "multipart/form-data")) {
			if err := c.Request.ParseForm(); err != nil {
				if strings.Contains(err.Error(), "http: request body too large") {
					c.AbortWithStatus(http.StatusRequestEntityTooLarge)
					return
				}
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
		}

		c.Next()
	}
}

func registerActionRoute(group *gin.RouterGroup, method, path string, handlers []gin.HandlerFunc) {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet:
		group.GET(path, handlers...)
	case http.MethodPost:
		group.POST(path, handlers...)
	default:
		group.Handle(method, path, handlers...)
	}
}

func appendActionHandler(middlewares []gin.HandlerFunc, handler gin.HandlerFunc) []gin.HandlerFunc {
	handlers := make([]gin.HandlerFunc, 0, len(middlewares)+1)
	handlers = append(handlers, middlewares...)
	handlers = append(handlers, handler)
	return handlers
}

func actionRelativePath(routePrefix, absolutePath string) string {
	relative := strings.TrimSpace(absolutePath)
	routePrefix = strings.TrimRight(strings.TrimSpace(routePrefix), "/")
	if routePrefix != "" && strings.HasPrefix(relative, routePrefix) {
		relative = strings.TrimPrefix(relative, routePrefix)
	}
	if relative == "" {
		return "/"
	}
	if !strings.HasPrefix(relative, "/") {
		return "/" + relative
	}
	return relative
}
