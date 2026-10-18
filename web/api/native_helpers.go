package api

import (
	"html"
	"strings"

	webservice "github.com/djylb/nps/web/service"
	"github.com/djylb/nps/web/ui"
)

func requestString(c Context, key string) string {
	return c.String(key)
}

func requestEscapedString(c Context, key string) string {
	return html.EscapeString(requestString(c, key))
}

// requestAccessLists reads the source access lists of a level from the
// fields prefix+"iplistpriority", prefix+"blackiplist", prefix+"whiteiplist"
// and prefix+"registered_only". The rules are validated by the service and
// escaped by the templates.
func requestAccessLists(c Context, prefix string) webservice.AccessListInput {
	return webservice.AccessListInput{
		Priority:       requestIntValue(c, prefix+"iplistpriority"),
		Black:          requestString(c, prefix+"blackiplist"),
		White:          requestString(c, prefix+"whiteiplist"),
		RegisteredOnly: requestBoolValue(c, prefix+"registered_only"),
	}
}

func requestIntValue(c Context, key string, def ...int) int {
	return c.Int(key, def...)
}

func requestBoolValue(c Context, key string, def ...bool) bool {
	return c.Bool(key, def...)
}

func ajax(c Context, msg string, status int) {
	c.RespondJSON(200, StatusMessageResponse{
		Status: status,
		Msg:    msg,
	})
}

func ajaxWithID(c Context, msg string, status, id int) {
	c.RespondJSON(200, StatusMessageIDResponse{
		Status: status,
		Msg:    msg,
		ID:     id,
	})
}

func ajaxWithNonce(c Context, msg string, status int, nonce string) {
	c.RespondJSON(200, StatusNonceResponse{
		Status: status,
		Msg:    msg,
		Nonce:  nonce,
	})
}

func ajaxWithNonceBits(c Context, msg string, status int, nonce string, bits int) {
	c.RespondJSON(200, StatusNonceBitsResponse{
		Status: status,
		Msg:    msg,
		Nonce:  nonce,
		Bits:   bits,
	})
}

func ajaxWithNonceCert(c Context, msg string, status int, nonce, cert string) {
	c.RespondJSON(200, StatusNonceCertResponse{
		Status: status,
		Msg:    msg,
		Nonce:  nonce,
		Cert:   cert,
	})
}

func ajaxWithNonceTimestamp(c Context, msg string, status int, nonce string, timestamp int64) {
	c.RespondJSON(200, StatusNonceTimestampResponse{
		Status:    status,
		Msg:       msg,
		Nonce:     nonce,
		Timestamp: timestamp,
	})
}

func respondCode(c Context, code int) {
	c.RespondJSON(200, CodeResponse{
		Code: code,
	})
}

func respondCodeData(c Context, code int, data any) {
	c.RespondJSON(200, CodeDataResponse{
		Code: code,
		Data: data,
	})
}

func respondCodeRTT(c Context, code, rtt int) {
	c.RespondJSON(200, CodeRTTResponse{
		Code: code,
		RTT:  rtt,
	})
}

func respondTable(c Context, rows any, total int) {
	c.RespondJSON(200, TableResponse{
		Rows:  rows,
		Total: total,
	})
}

func respondClientList(c Context, rows any, total int, bridgeIP, bridgeAddr, bridgeType string, bridgePort int) {
	c.RespondJSON(200, ClientListResponse{
		Rows:       rows,
		Total:      total,
		IP:         bridgeIP,
		Addr:       bridgeAddr,
		BridgeType: bridgeType,
		BridgePort: bridgePort,
	})
}

func sessionBool(c Context, key string) bool {
	value, _ := c.SessionValue(key).(bool)
	return value
}

func sessionString(c Context, key string) string {
	value, _ := c.SessionValue(key).(string)
	return value
}

func setSession(c Context, key string, value interface{}) {
	c.SetSessionValue(key, value)
}

func deleteSession(c Context, key string) {
	c.DeleteSessionValue(key)
}

func titleName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func newPage(controller, action, tpl string, data map[string]interface{}) *ui.Page {
	pageData := make(map[string]interface{}, len(data))
	for key, value := range data {
		pageData[key] = value
	}
	return &ui.Page{
		Controller: titleName(controller) + "Controller",
		Action:     titleName(action),
		Template:   tpl,
		Layout:     "public/layout.html",
		Data:       pageData,
	}
}

func newManagedSpecPage(spec PageSpec, data map[string]interface{}) *ui.Page {
	return withSpecDefaults(spec, newPage(spec.Controller, spec.Action, spec.Template, data))
}

func newStandaloneSpecPage(spec PageSpec, data map[string]interface{}) *ui.Page {
	return withSpecDefaults(spec, &ui.Page{
		Controller: titleName(spec.Controller) + "Controller",
		Action:     titleName(spec.Action),
		Template:   spec.Template,
		Data:       toPageData(data),
	})
}

func withSpecDefaults(spec PageSpec, page *ui.Page) *ui.Page {
	if page == nil {
		return nil
	}
	if page.Data == nil {
		page.Data = make(map[string]interface{})
	}
	if spec.Menu != "" {
		if _, ok := page.Data["menu"]; !ok {
			page.Data["menu"] = spec.Menu
		}
	}
	return page
}

func renderPage(c Context, page *ui.Page) {
	renderedHTML, err := ui.RenderToString(page)
	if err != nil {
		respondEmpty(c, 500)
		return
	}
	c.RespondData(200, "text/html; charset=utf-8", []byte(renderedHTML))
}

func renderPageModel(c Context, baseURL string, spec PageSpec, page *ui.Page) {
	c.RespondJSON(200, newPageModelResponse(baseURL, spec, page))
}

func respondEmpty(c Context, status int) {
	c.RespondData(status, "text/plain; charset=utf-8", nil)
}
