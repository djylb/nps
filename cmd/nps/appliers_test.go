package main

import (
	"net/http"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/web/routers"
)

// TestEverySettingHasAnApplier fails for a setting of the config snapshot
// that no applier of a reload owns, so that a new setting has to say whether
// a reload applies it or a restart does.
func TestEverySettingHasAnApplier(t *testing.T) {
	db := file.New(t.TempDir())
	srv := server.New(db, server.Config{})
	t.Cleanup(func() { _ = srv.Close() })
	srv.AddConfigApplier(routers.ConfigApplier(db, nil, srv, func(http.Handler) {}))
	appliers := srv.ConfigAppliers()
	if err := servercfg.CheckSettings(appliers); err != nil {
		t.Fatal(err)
	}
	for _, s := range servercfg.Unowned(appliers) {
		t.Errorf("no applier owns %s: add it to the applier that applies it, or to one that reports a restart", s)
	}
}
