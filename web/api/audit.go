package api

import (
	"net/http"

	"github.com/djylb/nps/lib/logevent"
	"github.com/djylb/slogx"
)

// auditContext is the Context of an audited action: it notes the answer the
// handler gives, so that the action is written once, as one admin_action
// event, when the handler returns.
type auditContext struct {
	Context
	responded bool
	ok        bool
	reason    string
	created   int
	user      string
	client    int
}

func (c *auditContext) note(httpStatus int, ok bool) {
	if c.responded {
		return
	}
	c.responded = true
	c.ok = ok && httpStatus < http.StatusBadRequest
	switch {
	case c.ok:
	case httpStatus >= http.StatusInternalServerError:
		c.reason = "error"
	case httpStatus >= http.StatusBadRequest:
		c.reason = "bad_request"
	default:
		c.reason = "rejected"
	}
}

func (c *auditContext) RespondJSON(status int, value any) {
	ok := true
	switch v := value.(type) {
	case StatusMessageResponse:
		ok = v.Status == 1
	case StatusMessageIDResponse:
		ok = v.Status == 1
		if ok {
			c.created = v.ID
		}
	case StatusNonceResponse:
		ok = v.Status == 1
	case ReloadConfigResponse:
		ok = v.Status == 1
	case CodeResponse:
		ok = v.Code == 1
	}
	c.note(status, ok)
	c.Context.RespondJSON(status, value)
}

func (c *auditContext) RespondString(status int, body string) {
	c.note(status, true)
	c.Context.RespondString(status, body)
}

func (c *auditContext) RespondData(status int, contentType string, data []byte) {
	c.note(status, true)
	c.Context.RespondData(status, contentType, data)
}

func (c *auditContext) Redirect(status int, location string) {
	c.note(status, true)
	c.Context.Redirect(status, location)
}

// auditNote sets the user and client of the audited action of c, for an
// action whose caller is not logged in, such as registering.
func auditNote(c Context, user string, client int) {
	if ac, ok := c.(*auditContext); ok {
		ac.user, ac.client = user, client
	}
}

// audited wraps the handler of an action that changes state so that it
// writes the admin_action event of the action.
func (a *App) audited(spec ActionSpec) Handler {
	next := spec.Handler
	return func(c Context) {
		ac := &auditContext{Context: c}
		id := c.Int("id")
		client := c.Int("client_id")
		var tunnel, host int
		switch spec.Ownership {
		case ActionOwnershipClient:
			client = id
		case ActionOwnershipTunnel:
			tunnel = id
			if a.Services.Index != nil && id > 0 {
				if t, err := a.Services.Index.GetTunnel(id); err == nil && t != nil && t.Client != nil {
					client = t.Client.Id
				}
			}
		case ActionOwnershipHost:
			host = id
			if a.Services.Index != nil && id > 0 {
				if h, err := a.Services.Index.GetHost(id); err == nil && h != nil && h.Client != nil {
					client = h.Client.Id
				}
			}
		}
		next(ac)
		if ac.created > 0 {
			switch spec.Audit {
			case "client_create":
				client = ac.created
			case "tunnel_create":
				tunnel = ac.created
			case "host_create":
				host = ac.created
			}
		}
		user := actorName(c)
		if ac.user != "" {
			user = ac.user
		}
		if ac.client != 0 {
			client = ac.client
		}
		action := spec.Audit
		if spec.Controller == "client" && spec.Name == "changestatus" {
			action = "client_disable"
			if c.Bool("status") {
				action = "client_enable"
			}
		}
		args := []any{"src_ip", VisitorIP(c, a.currentConfig()), "user", user, "action", action}
		if client != 0 {
			args = append(args, "client", client)
		}
		if tunnel != 0 {
			args = append(args, "tunnel", tunnel)
		}
		if host != 0 {
			args = append(args, "host", host)
		}
		if ac.ok || !ac.responded {
			args = append(args, "result", "ok")
		} else {
			args = append(args, "result", "failed", "reason", ac.reason)
		}
		slogx.Emit(c.BaseContext(), log, &logevent.AdminAction, args...)
	}
}

// withAudit wraps the handlers of the specs that name an audit action.
func (a *App) withAudit(specs []ActionSpec) []ActionSpec {
	for i := range specs {
		if specs[i].Audit != "" && specs[i].Handler != nil {
			specs[i].Handler = a.audited(specs[i])
		}
	}
	return specs
}
