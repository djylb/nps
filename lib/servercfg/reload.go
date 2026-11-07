package servercfg

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// ErrRestartRequired is what Apply of an Applier returns for settings that
// are only read at startup: a reload keeps them as they were.
var ErrRestartRequired = errors.New("restart required")

// RestartRequired is the Apply of the settings only read at startup.
func RestartRequired(_, _ *Snapshot) error { return ErrRestartRequired }

// An Applier owns some settings of the Snapshot and says how a reload makes
// their changes take effect. Every setting is owned by exactly one Applier
// (CheckAppliers), so that the Appliers are the one record of what a reload
// applies.
type Applier struct {
	// Name names the Applier in reports.
	Name string
	// Settings are the fields the Applier owns, as paths of the Snapshot
	// such as "Log.Level"; a struct field owns all the fields within it.
	Settings []string
	// Apply makes the changes of the settings take effect once cur is the
	// current snapshot; it is called only when one of them differs between
	// old and cur. Nil means the settings are read where they are used, so
	// that they take effect with the snapshot. RestartRequired reports
	// settings that take effect at the next start.
	Apply func(old, cur *Snapshot) error
}

// Failure is an Applier that failed in a reload.
type Failure struct {
	Name     string   `json:"name"`
	Settings []string `json:"settings"`
	Error    string   `json:"error"`
}

// Report is the outcome of a reload. When Error is set, the file could not
// be read and nothing changed. When Failed is not empty, the reload was
// undone: the previous snapshot is current again and the Appliers that had
// applied it were applied back, RollbackFailed lists those that could not.
type Report struct {
	Path           string    `json:"path"`
	Error          string    `json:"error,omitempty"`
	Applied        []string  `json:"applied"`
	Restart        []string  `json:"restart"`
	Failed         []Failure `json:"failed"`
	RollbackFailed []Failure `json:"rollback_failed,omitempty"`
}

// OK reports whether the new settings are current.
func (r Report) OK() bool { return r.Error == "" && len(r.Failed) == 0 }

// String sums the report up in one line.
func (r Report) String() string {
	switch {
	case r.Error != "":
		return "config reload failed, the previous settings stay: " + r.Error
	case len(r.Failed) > 0:
		var parts []string
		for _, f := range r.Failed {
			parts = append(parts, fmt.Sprintf("%s (%s): %s", f.Name, strings.Join(f.Settings, ", "), f.Error))
		}
		s := "config reload failed, the previous settings were restored: " + strings.Join(parts, "; ")
		for _, f := range r.RollbackFailed {
			s += fmt.Sprintf("; restoring %s failed: %s", f.Name, f.Error)
		}
		return s
	}
	s := "config reloaded from " + r.Path
	if len(r.Applied) == 0 && len(r.Restart) == 0 {
		return s + ", nothing changed"
	}
	if len(r.Applied) > 0 {
		s += "; applied: " + strings.Join(r.Applied, ", ")
	}
	if len(r.Restart) > 0 {
		s += "; these changes need a restart: " + strings.Join(r.Restart, ", ")
	}
	return s
}

var reloadMu sync.Mutex

// Reload reads the loaded config file again, or the default candidates when
// none is loaded, and hands the changes to the appliers. A file that cannot
// be read changes nothing. Otherwise the new snapshot becomes current and
// the appliers whose settings changed are applied in order. When one fails,
// the previous snapshot becomes current again and those applied before are
// applied back in reverse order, so that a reload takes effect as a whole or
// not at all. Reloads run one at a time.
func Reload(appliers ...Applier) Report {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	if err := CheckSettings(appliers); err != nil {
		return Report{Path: Path(), Error: err.Error()}
	}
	path, next, err := readCurrent()
	if err != nil {
		return Report{Path: path, Error: err.Error()}
	}
	prevPath, prev := Path(), current()
	old := Current()
	cur := next.snapshot
	r := Report{Path: path, Applied: []string{}, Restart: []string{}, Failed: []Failure{}}
	commit(path, next)
	var done []Applier
	for _, a := range appliers {
		changed := changedSettings(a.Settings, old, cur)
		if len(changed) == 0 {
			continue
		}
		if a.Apply == nil {
			r.Applied = append(r.Applied, changed...)
			continue
		}
		err := a.Apply(old, cur)
		switch {
		case err == nil:
			r.Applied = append(r.Applied, changed...)
			done = append(done, a)
		case errors.Is(err, ErrRestartRequired):
			r.Restart = append(r.Restart, changed...)
		default:
			r.Failed = append(r.Failed, Failure{Name: a.Name, Settings: changed, Error: err.Error()})
		}
		if len(r.Failed) > 0 {
			break
		}
	}
	if len(r.Failed) == 0 {
		return r
	}
	if prev != nil {
		commit(prevPath, prev)
	} else {
		currentSnapshot.Store(old)
	}
	for _, a := range slices.Backward(done) {
		if err := a.Apply(cur, old); err != nil {
			r.RollbackFailed = append(r.RollbackFailed, Failure{Name: a.Name, Settings: changedSettings(a.Settings, old, cur), Error: err.Error()})
		}
	}
	r.Applied, r.Restart = []string{}, []string{}
	return r
}

func readCurrent() (string, *store, error) {
	if path := Path(); path != "" {
		cfg, err := read(path)
		return path, cfg, err
	}
	var errs []error
	for _, path := range dedupePaths(DefaultPaths()...) {
		cfg, err := read(path)
		if err == nil {
			return path, cfg, nil
		}
		errs = append(errs, fmt.Errorf("load %s: %w", path, err))
	}
	if len(errs) == 0 {
		return "", nil, errors.New("no server config path provided")
	}
	return "", nil, errors.Join(errs...)
}

// changedSettings returns the settings that differ between old and cur.
func changedSettings(settings []string, old, cur *Snapshot) []string {
	var changed []string
	for _, s := range settings {
		if !reflect.DeepEqual(settingValue(old, s).Interface(), settingValue(cur, s).Interface()) {
			changed = append(changed, s)
		}
	}
	return changed
}

func settingValue(cfg *Snapshot, path string) reflect.Value {
	v := reflect.ValueOf(cfg).Elem()
	for name := range strings.SplitSeq(path, ".") {
		v = v.FieldByName(name)
	}
	return v
}

// Settings returns the paths of all the settings of the Snapshot: its
// fields that are not structs, and those within the struct fields.
func Settings() []string {
	var paths []string
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for f := range t.Fields() {
			path := prefix + f.Name
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, path+".")
				continue
			}
			paths = append(paths, path)
		}
	}
	walk(reflect.TypeFor[Snapshot](), "")
	return paths
}

// CheckSettings returns an error for a setting of the appliers that is not
// a field of the Snapshot or that two of them own.
func CheckSettings(appliers []Applier) error {
	owner := map[string]string{}
	var errs []error
	for _, a := range appliers {
		for _, s := range a.Settings {
			if !validSetting(s) {
				errs = append(errs, fmt.Errorf("%s: %q is not a setting", a.Name, s))
				continue
			}
			for o, by := range owner {
				if o == s || strings.HasPrefix(o, s+".") || strings.HasPrefix(s, o+".") {
					errs = append(errs, fmt.Errorf("%q of %s overlaps %q of %s", s, a.Name, o, by))
				}
			}
			owner[s] = a.Name
		}
	}
	return errors.Join(errs...)
}

// Unowned returns the settings of the Snapshot that no applier owns.
func Unowned(appliers []Applier) []string {
	var missing []string
	for _, s := range Settings() {
		owned := false
		for _, a := range appliers {
			for _, o := range a.Settings {
				if o == s || strings.HasPrefix(s, o+".") {
					owned = true
				}
			}
		}
		if !owned {
			missing = append(missing, s)
		}
	}
	return missing
}

func validSetting(path string) bool {
	t := reflect.TypeFor[Snapshot]()
	for name := range strings.SplitSeq(path, ".") {
		if t.Kind() != reflect.Struct {
			return false
		}
		f, ok := t.FieldByName(name)
		if !ok || !f.IsExported() {
			return false
		}
		t = f.Type
	}
	return path != ""
}
