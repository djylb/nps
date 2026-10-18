package routers

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func sessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := framework.EnsureSession(c); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			c.Abort()
			return
		}
		c.Next()
	}
}

func apiInputMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
		if !strings.HasPrefix(contentType, "application/json") || c.Request.Body == nil {
			c.Next()
			return
		}

		payload := make(map[string]interface{})
		decoder := json.NewDecoder(c.Request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			if err == io.EOF {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, webapi.StatusMessageResponse{
				Status: 0,
				Msg:    "invalid json body",
			})
			return
		}
		for key, value := range payload {
			text := stringifyAPIValue(value)
			framework.SetRequestInput(c, key, text)
			// Values already normalized by earlier middleware take precedence.
			if _, exists := framework.RequestParam(c, key); exists {
				continue
			}
			framework.SetRequestParam(c, key, text)
		}
		c.Next()
	}
}

func requestContextMiddleware(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		attachRequestMetadata(c, state.App)
		ensureAutoAdminSession(c, state)
		setActor(c, actorFromSession(c, state.PermissionResolver(), state.AdminUsername()))
		c.Next()
	}
}

func ensureAutoAdminSession(c *gin.Context, state *State) {
	if framework.SessionValue(c, "auth") == true {
		return
	}
	identity, ok := webservice.AutoAdminIdentity(state.CurrentConfig())
	if !ok {
		return
	}
	if err := webapi.ApplySessionIdentity(newAPIContext(c), identity); err != nil {
		logs.Error("Automatic administrator session could not be saved: %v", err)
		return
	}
	state.System().RegisterManagementAccess(c.ClientIP(), nil, true)
}

func protectedRouteMiddleware(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := state.CurrentConfig()

		timestamp, _ := strconv.Atoi(c.Query("timestamp"))
		if !webservice.ValidAuthKey(cfg.Auth.Key, c.Query("auth_key"), timestamp, common.TimeNow().Unix()) {
			if framework.SessionValue(c, "auth") != true {
				abortUnauthorized(c, state.LoginURL())
				return
			}
		} else {
			if err := framework.SetSessionValue(c, "isAdmin", true); err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				c.Abort()
				return
			}
			setActor(c, webapi.AdminActorWithFallback("", cfg.Web.Username))
		}

		actor := currentActor(c)
		if actor.IsAdmin {
			framework.SetRequestData(c, "isAdmin", true)
			c.Next()
			return
		}

		framework.SetRequestData(c, "client_ids", actor.ClientIDs)
		if clientID, ok := webapi.ActorPrimaryClientID(actor); ok {
			framework.SetRequestData(c, "client_id", clientID)
			framework.SetRequestParam(c, "client_id", strconv.Itoa(clientID))
		}
		framework.SetRequestData(c, "isAdmin", false)
		if actor.Username != "" {
			framework.SetRequestData(c, "username", actor.Username)
		}
		c.Next()
	}
}

func permissionMiddleware(state *State, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := state.Authorization().RequirePermission(currentPrincipal(c), permission); err != nil {
			abortAccessError(c, state, err)
			return
		}
		c.Next()
	}
}

// clientScopeMiddleware resolves the client_id submitted with the request
// and stores the normalized value for the handler. When keepUnset is true an
// empty or non-positive client_id is normalized to 0 so the handler keeps the
// resource's current client.
func clientScopeMiddleware(state *State, keepUnset bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		requested, err := requestedClientID(c)
		if err != nil {
			abortForbidden(c)
			return
		}
		if keepUnset && requested <= 0 {
			framework.SetRequestParam(c, "client_id", "0")
			c.Next()
			return
		}
		resolvedClientID, err := state.Authorization().ResolveClient(currentPrincipal(c), requested)
		if err != nil {
			abortAccessError(c, state, err)
			return
		}
		if resolvedClientID > 0 {
			framework.SetRequestData(c, "client_id", resolvedClientID)
		}
		framework.SetRequestParam(c, "client_id", strconv.Itoa(resolvedClientID))
		c.Next()
	}
}

func clientOwnershipMiddleware(state *State) gin.HandlerFunc {
	return resourceOwnershipMiddleware(state, state.Authorization().RequireClient)
}

func tunnelOwnershipMiddleware(state *State) gin.HandlerFunc {
	return resourceOwnershipMiddleware(state, state.Authorization().RequireTunnel)
}

func hostOwnershipMiddleware(state *State) gin.HandlerFunc {
	return resourceOwnershipMiddleware(state, state.Authorization().RequireHost)
}

func resourceOwnershipMiddleware(state *State, require func(webservice.Principal, int) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok, err := requestInt(c, "id")
		if err != nil {
			abortForbidden(c)
			return
		}
		if !ok {
			c.Next()
			return
		}
		if err := require(currentPrincipal(c), id); err != nil {
			abortAccessError(c, state, err)
			return
		}
		c.Next()
	}
}

// requestInt reads key with the same lookup order and parsing rules as the
// handler context and stores the normalized value back as a request param.
// It reports whether a non-empty value was present.
func requestInt(c *gin.Context, key string) (int, bool, error) {
	raw := strings.TrimSpace(newAPIContext(c).String(key))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, err
	}
	framework.SetRequestParam(c, key, strconv.Itoa(value))
	return value, true, nil
}

// requestedClientID returns the client_id submitted by the caller from the
// form or the JSON body; an absent value yields 0.
func requestedClientID(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.Request.FormValue("client_id"))
	if raw == "" {
		if input, ok := framework.RequestInput(c, "client_id"); ok {
			raw = strings.TrimSpace(input)
		}
	}
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}

func stringifyAPIValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case json.Number:
		return v.String()
	case []interface{}, map[string]interface{}:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
	}
	if data, err := json.Marshal(value); err == nil {
		text := string(data)
		if text != "null" {
			return text
		}
	}
	return ""
}

func abortUnauthorized(c *gin.Context, loginURL string) {
	if isMachineAPIRequest(c) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, webapi.StatusMessageResponse{
			Status: 0,
			Msg:    "unauthorized",
		})
		return
	}
	c.Redirect(http.StatusFound, loginURL)
	c.Abort()
}

func abortForbidden(c *gin.Context) {
	if isMachineAPIRequest(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, webapi.StatusMessageResponse{
			Status: 0,
			Msg:    "forbidden",
		})
		return
	}
	c.AbortWithStatus(http.StatusForbidden)
}

func abortAccessError(c *gin.Context, state *State, err error) {
	loginURL := "/login/index"
	if state != nil {
		loginURL = state.LoginURL()
	}
	switch {
	case errors.Is(err, webservice.ErrUnauthenticated):
		abortUnauthorized(c, loginURL)
	case errors.Is(err, webservice.ErrForbidden):
		abortForbidden(c)
	default:
		abortForbidden(c)
	}
}

func isMachineAPIRequest(c *gin.Context) bool {
	path := c.Request.URL.Path
	return strings.Contains(path, "/api/v1/")
}

func currentPrincipal(c *gin.Context) webservice.Principal {
	return webapi.PrincipalFromActor(currentActor(c))
}

// globalFirewallMiddleware applies the global access lists, the firewall in
// front of nps, to every request of the web interface, static files and the
// login page included. The visitor address is read as the login reads it.
func globalFirewallMiddleware(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		if file.GetDb().GlobalDeniedString(webClientIP(c, state.CurrentConfig()), "Web") {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// webClientIP returns the visitor address of a web request: the peer, or
// the X-Real-IP header a trusted proxy sets.
func webClientIP(c *gin.Context, cfg *servercfg.Snapshot) string {
	ip := c.Request.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	pass := cfg.Auth.HTTPOnlyPass
	if (cfg.Auth.AllowXRealIP && common.IsTrustedProxy(cfg.Auth.TrustedProxyIPs, ip)) ||
		(pass != "" && c.GetHeader("X-NPS-Http-Only") == pass) {
		if realIP := strings.TrimSpace(c.GetHeader("X-Real-IP")); realIP != "" {
			ip = realIP
		}
	}
	return ip
}
