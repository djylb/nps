package file

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
)

func newTestDb(t *testing.T) *DbUtils {
	t.Helper()
	resetTaskPasswords()
	t.Cleanup(resetTaskPasswords)
	dir := t.TempDir()
	return &DbUtils{JsonDb: &JsonDb{TaskFilePath: filepath.Join(dir, "tasks.json")}}
}

func passwordOwner(pwd string) (int, bool) {
	return TaskPasswordIndex.Get(crypt.Md5(pwd))
}

func TestTaskPasswordConflicts(t *testing.T) {
	db := newTestDb(t)

	if err := db.NewTask(&Tunnel{Id: 1, Mode: "secret", Password: "shared"}); err != nil {
		t.Fatalf("NewTask(1): %v", err)
	}

	web := &Tunnel{Id: 2, Mode: "p2p", Password: "shared"}
	if err := db.NewTask(web); err == nil {
		t.Fatalf("explicit duplicate password from the web must be rejected")
	}
	if web.Password != "shared" {
		t.Fatalf("rejected password must not be replaced, got %q", web.Password)
	}

	client := &Tunnel{Id: 3, Mode: "secret", Password: "shared", NoStore: true}
	if err := db.NewTask(client); err != nil {
		t.Fatalf("client task must not fail on a duplicate password: %v", err)
	}
	if client.Password == "shared" {
		t.Fatalf("client task password must be replaced on conflict")
	}
	if id, ok := passwordOwner(client.Password); !ok || id != 3 {
		t.Fatalf("replacement password not claimed: %d %v", id, ok)
	}

	generated := &Tunnel{Id: 4, Mode: "secret"}
	if err := db.NewTask(generated); err != nil {
		t.Fatalf("NewTask(generated): %v", err)
	}
	if id, ok := passwordOwner(generated.Password); generated.Password == "" || !ok || id != 4 {
		t.Fatalf("generated password not claimed: %q %d %v", generated.Password, id, ok)
	}
}

func TestUpdateTaskInPlaceReleasesOldPassword(t *testing.T) {
	db := newTestDb(t)
	task := &Tunnel{Id: 1, Mode: "secret", Password: "old"}
	if err := db.NewTask(task); err != nil {
		t.Fatal(err)
	}
	if err := db.NewTask(&Tunnel{Id: 2, Mode: "secret", Password: "taken"}); err != nil {
		t.Fatal(err)
	}

	// Editing the stored object itself must still release the old password.
	task.Password = "new"
	if err := db.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, ok := passwordOwner("old"); ok {
		t.Fatalf("old password still claimed after in-place update")
	}
	if id, ok := passwordOwner("new"); !ok || id != 1 {
		t.Fatalf("new password not claimed: %d %v", id, ok)
	}

	// A rejected edit made on a copy leaves the stored task and index unchanged.
	edited := task.Clone()
	edited.Password = "taken"
	if err := db.UpdateTask(edited); err == nil {
		t.Fatalf("conflicting update must be rejected")
	}
	if v, _ := db.JsonDb.Tasks.Load(1); v.(*Tunnel) != task || task.Password != "new" {
		t.Fatalf("stored task changed by a rejected update")
	}
	if id, ok := passwordOwner("new"); !ok || id != 1 {
		t.Fatalf("password claim lost after rejected update: %d %v", id, ok)
	}

	if err := db.DelTask(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := passwordOwner("new"); ok {
		t.Fatalf("password still claimed after DelTask")
	}
}

func TestLoadedDuplicatePasswordStaysEditable(t *testing.T) {
	db := newTestDb(t)
	first := &Tunnel{Id: 1, Mode: "secret", Password: "dup"}
	second := &Tunnel{Id: 2, Mode: "secret", Password: "dup"}
	// Same path as LoadTaskFromJsonFile.
	for _, task := range []*Tunnel{first, second} {
		_ = reserveTaskPassword(task, false)
		db.JsonDb.Tasks.Store(task.Id, task)
	}
	if first.Password != "dup" || second.Password == "dup" {
		t.Fatalf("expected only the later duplicate to be replaced: %q %q", first.Password, second.Password)
	}
	second.Status = true
	if err := db.UpdateTask(second); err != nil {
		t.Fatalf("UpdateTask on a deduplicated task: %v", err)
	}
}

func TestTunnelCloneCopiesAllFields(t *testing.T) {
	src := &Tunnel{}
	v := reflect.ValueOf(src).Elem()
	fill(t, v)
	src.Flow.ExportFlow, src.Flow.InletFlow, src.Flow.FlowLimit, src.Flow.TimeLimit = 1, 2, 3, time.Unix(4, 0)
	c := src.Clone()
	if c.Flow == src.Flow {
		t.Fatalf("Clone must copy Flow")
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if !f.IsExported() || f.Name == "RWMutex" {
			continue
		}
		got := reflect.ValueOf(c).Elem().Field(i).Interface()
		if f.Name == "Health" {
			got = healthFields(c)
			if !reflect.DeepEqual(got, healthFields(src)) {
				t.Fatalf("Clone lost health fields")
			}
			continue
		}
		if f.Name == "Flow" {
			if c.Flow.ExportFlow != src.Flow.ExportFlow || c.Flow.InletFlow != src.Flow.InletFlow ||
				c.Flow.FlowLimit != src.Flow.FlowLimit || !c.Flow.TimeLimit.Equal(src.Flow.TimeLimit) {
				t.Fatalf("Clone lost flow values")
			}
			continue
		}
		if !reflect.DeepEqual(got, v.Field(i).Interface()) {
			t.Fatalf("Clone did not copy field %s", f.Name)
		}
	}
}

type healthSnapshot struct {
	Timeout, MaxFail, Interval int
	Next                       time.Time
	Map                        map[string]int
	URL                        string
	Removed                    []string
	Type, Target               string
}

func healthFields(t *Tunnel) healthSnapshot {
	return healthSnapshot{t.HealthCheckTimeout, t.HealthMaxFail, t.HealthCheckInterval, t.HealthNextTime, t.HealthMap, t.HttpHealthUrl, t.HealthRemoveArr, t.HealthCheckType, t.HealthCheckTarget}
}

// fill sets every exported field of v to a non-zero value.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		sf := v.Type().Field(i)
		if !sf.IsExported() || !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			f.SetInt(int64(i + 1))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.String:
			f.SetString(sf.Name)
		case reflect.Ptr:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Map:
			f.Set(reflect.MakeMap(f.Type()))
			f.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf(1))
		case reflect.Slice:
			f.Set(reflect.Append(reflect.MakeSlice(f.Type(), 0, 1), reflect.ValueOf("x")))
		case reflect.Struct:
			if sf.Type == reflect.TypeOf(time.Time{}) {
				f.Set(reflect.ValueOf(time.Unix(int64(i+1), 0)))
			} else if sf.Anonymous && sf.Name == "Health" {
				fill(t, f)
			}
		}
	}
}
