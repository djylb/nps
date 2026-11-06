package routers

import (
	"strconv"
	"strings"

	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

const (
	actorContextKey    = "nps.api.actor"
	metadataContextKey = "nps.api.metadata"
)

func attachRequestMetadata(c *gin.Context, app *webapi.App) {
	if app == nil {
		return
	}
	requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if requestID == "" {
		requestID = strings.TrimSpace(c.GetHeader("X-Correlation-ID"))
	}
	source := strings.TrimSpace(c.GetHeader("X-NPS-Source"))
	if source == "" {
		source = strings.TrimSpace(c.GetHeader("User-Agent"))
	}
	setRequestMetadata(c, webapi.RequestMetadata{
		NodeID:    app.NodeID,
		RequestID: requestID,
		Source:    source,
	})
}

func setRequestMetadata(c *gin.Context, metadata webapi.RequestMetadata) {
	c.Set(metadataContextKey, metadata)
}

func currentRequestMetadata(c *gin.Context) webapi.RequestMetadata {
	if v, ok := c.Get(metadataContextKey); ok {
		if metadata, ok := v.(webapi.RequestMetadata); ok {
			return metadata
		}
	}
	return webapi.RequestMetadata{}
}

func actorFromSession(c *gin.Context, resolver webservice.PermissionResolver, fallbackAdminUsername string) *webapi.Actor {
	if identity := sessionIdentityFromSession(c, resolver); identity != nil && identity.Authenticated {
		return webapi.ActorFromSessionIdentityWithFallback(identity, fallbackAdminUsername)
	}
	if framework.SessionValue(c, "auth") != true {
		return webapi.AnonymousActor()
	}
	if isAdmin, _ := framework.SessionValue(c, "isAdmin").(bool); isAdmin {
		username, _ := framework.SessionValue(c, "username").(string)
		return webapi.AdminActorWithFallback(username, fallbackAdminUsername)
	}

	username, _ := framework.SessionValue(c, "username").(string)
	clientIDs := sessionClientIDs(c)
	if len(clientIDs) == 0 {
		if clientID, _ := framework.SessionValue(c, "clientId").(int); clientID > 0 {
			clientIDs = append(clientIDs, clientID)
		}
	}
	return webapi.UserActor(username, clientIDs)
}

func sessionIdentityFromSession(c *gin.Context, resolver webservice.PermissionResolver) *webservice.SessionIdentity {
	raw, _ := framework.SessionValue(c, webservice.SessionIdentityKey).(string)
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, resolver)
	if err != nil {
		return nil
	}
	return identity
}

func setActor(c *gin.Context, actor *webapi.Actor) {
	if actor == nil {
		actor = webapi.AnonymousActor()
	}
	c.Set(actorContextKey, actor)
}

func currentActor(c *gin.Context) *webapi.Actor {
	if v, ok := c.Get(actorContextKey); ok {
		if actor, ok := v.(*webapi.Actor); ok && actor != nil {
			return actor
		}
	}
	return webapi.AnonymousActor()
}

func sessionClientIDs(c *gin.Context) []int {
	switch value := framework.SessionValue(c, "clientIds").(type) {
	case []int:
		return append([]int(nil), value...)
	case []interface{}:
		clientIDs := make([]int, 0, len(value))
		for _, current := range value {
			switch typed := current.(type) {
			case int:
				if typed > 0 {
					clientIDs = append(clientIDs, typed)
				}
			case float64:
				if typed > 0 {
					clientIDs = append(clientIDs, int(typed))
				}
			case string:
				if clientID, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && clientID > 0 {
					clientIDs = append(clientIDs, clientID)
				}
			}
		}
		return clientIDs
	case string:
		parts := strings.Split(value, ",")
		clientIDs := make([]int, 0, len(parts))
		for _, part := range parts {
			clientID, err := strconv.Atoi(strings.TrimSpace(part))
			if err == nil && clientID > 0 {
				clientIDs = append(clientIDs, clientID)
			}
		}
		return clientIDs
	default:
		return nil
	}
}
