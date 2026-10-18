package api

import "context"

type Handler func(Context)

type Context interface {
	BaseContext() context.Context
	String(string) string
	Int(string, ...int) int
	Bool(string, ...bool) bool
	Method() string
	Host() string
	RemoteAddr() string
	ClientIP() string
	RequestHeader(string) string
	SessionValue(string) interface{}
	// SetSessionValue and DeleteSessionValue change the session and save it
	// in the response; an error says that it could not be saved.
	SetSessionValue(string, interface{}) error
	DeleteSessionValue(string) error
	RespondJSON(int, interface{})
	RespondString(int, string)
	RespondData(int, string, []byte)
	Redirect(int, string)
	SetResponseHeader(string, string)
	IsWritten() bool
	Actor() *Actor
	SetActor(*Actor)
	Metadata() RequestMetadata
}
