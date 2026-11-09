package api

import (
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

type cryptContext struct {
	Context
	values map[string]string
}

func (c cryptContext) String(k string) string { return c.values[k] }
func (c cryptContext) Int(string, ...int) int { return 0 }
func (c cryptContext) Bool(k string, def ...bool) bool {
	switch c.values[k] {
	case "":
		return len(def) > 0 && def[0]
	case "0", "false":
		return false
	}
	return true
}
func (c cryptContext) SessionValue(string) any { return nil }
func (c cryptContext) RespondJSON(int, any)    {}

type cryptClients struct {
	webservice.ClientService
	added  webservice.AddClientInput
	edited webservice.EditClientInput
}

func (s *cryptClients) Add(in webservice.AddClientInput) (int, error) { s.added = in; return 1, nil }
func (s *cryptClients) Edit(in webservice.EditClientInput) error      { s.edited = in; return nil }

// A new client encrypts unless the request says crypt=0; an edit without
// crypt leaves the stored setting alone.
func TestClientCryptDefaults(t *testing.T) {
	s := &cryptClients{}
	app := &App{Services: webservice.Services{Clients: s}}
	for _, tc := range []struct {
		crypt string
		add   bool
		edit  *bool
	}{{"", true, nil}, {"0", false, ptr(false)}, {"1", true, ptr(true)}} {
		values := map[string]string{"id": "1"}
		if tc.crypt != "" {
			values["crypt"] = tc.crypt
		}
		app.AddClient(cryptContext{values: values})
		app.EditClient(cryptContext{values: values})
		if s.added.Crypt != tc.add {
			t.Fatalf("crypt=%q: add %v", tc.crypt, s.added.Crypt)
		}
		if (s.edited.Crypt == nil) != (tc.edit == nil) || tc.edit != nil && *s.edited.Crypt != *tc.edit {
			t.Fatalf("crypt=%q: edit %v", tc.crypt, s.edited.Crypt)
		}
	}
}

func ptr(v bool) *bool { return &v }
