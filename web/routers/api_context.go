package routers

import (
	"context"
	"strconv"
	"strings"

	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	"github.com/gin-gonic/gin"
)

type ginAPIContext struct {
	gin *gin.Context
}

func newAPIContext(g *gin.Context) webapi.Context {
	return &ginAPIContext{gin: g}
}

func (c *ginAPIContext) BaseContext() context.Context {
	return c.gin.Request.Context()
}

func (c *ginAPIContext) String(key string) string {
	if value, ok := framework.RequestParam(c.gin, key); ok {
		return value
	}
	if value := c.gin.Param(key); value != "" {
		return value
	}
	return c.gin.Request.FormValue(key)
}

func (c *ginAPIContext) Int(key string, def ...int) int {
	value := strings.TrimSpace(c.String(key))
	if value == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func (c *ginAPIContext) Bool(key string, def ...bool) bool {
	value := strings.TrimSpace(c.String(key))
	if value == "" {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func (c *ginAPIContext) Method() string {
	return c.gin.Request.Method
}

func (c *ginAPIContext) Host() string {
	return c.gin.Request.Host
}

func (c *ginAPIContext) RemoteAddr() string {
	return c.gin.Request.RemoteAddr
}

func (c *ginAPIContext) ClientIP() string {
	return c.gin.ClientIP()
}

func (c *ginAPIContext) RequestHeader(key string) string {
	return c.gin.GetHeader(key)
}

func (c *ginAPIContext) SessionValue(key string) interface{} {
	return framework.SessionValue(c.gin, key)
}

func (c *ginAPIContext) SetSessionValue(key string, value interface{}) error {
	return framework.SetSessionValue(c.gin, key, value)
}

func (c *ginAPIContext) DeleteSessionValue(key string) error {
	return framework.DeleteSessionValue(c.gin, key)
}

func (c *ginAPIContext) RespondJSON(status int, value interface{}) {
	c.gin.JSON(status, value)
}

func (c *ginAPIContext) RespondString(status int, body string) {
	c.gin.String(status, body)
}

func (c *ginAPIContext) RespondData(status int, contentType string, data []byte) {
	c.gin.Data(status, contentType, data)
}

func (c *ginAPIContext) Redirect(status int, location string) {
	c.gin.Redirect(status, location)
}

func (c *ginAPIContext) SetResponseHeader(key, value string) {
	c.gin.Writer.Header().Set(key, value)
}

func (c *ginAPIContext) IsWritten() bool {
	return c.gin.Writer.Written()
}

func (c *ginAPIContext) Actor() *webapi.Actor {
	return currentActor(c.gin)
}

func (c *ginAPIContext) SetActor(actor *webapi.Actor) {
	setActor(c.gin, actor)
}

func (c *ginAPIContext) Metadata() webapi.RequestMetadata {
	return currentRequestMetadata(c.gin)
}
