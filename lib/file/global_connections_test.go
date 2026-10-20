package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalConnectionLimitPersistenceAndOldData(t *testing.T) {
	_, oldLimit := GlobalConnections()
	t.Cleanup(func() { SetGlobalMaxConn(int(oldLimit)) })
	db := newTestDb(t)
	db.JsonDb.GlobalFilePath = filepath.Join(t.TempDir(), "global.json")
	if err := db.SaveGlobal(&Glob{MaxConn: 9}); err != nil {
		t.Fatal(err)
	}
	SetGlobalMaxConn(0)
	db.JsonDb.LoadGlobalFromJsonFile()
	if _, limit := GlobalConnections(); limit != 9 || db.JsonDb.Global.MaxConn != 9 {
		t.Fatal("global connection limit not restored")
	}
	if err := os.WriteFile(db.JsonDb.GlobalFilePath, []byte(`{"RateLimit":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	db.JsonDb.LoadGlobalFromJsonFile()
	if _, limit := GlobalConnections(); limit != 0 {
		t.Fatal("old global configuration was not unlimited")
	}
}
