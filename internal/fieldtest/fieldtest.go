// Package fieldtest helps tests check that code handling every field of a
// struct keeps up with fields added to it.
package fieldtest

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Fill sets every exported field of the struct v points to, and of the
// structs its pointer fields lead to, to a value that differs from the zero
// value and from the other fields; structs filled with different seeds differ
// in every field, and an even seed sets the booleans. Fields of the sync packages are left alone.
func Fill(v any, seed int) {
	n := seed
	fill(reflect.ValueOf(v).Elem(), &n, 0, seed%2 == 0)
}

var timeType = reflect.TypeFor[time.Time]()

func fill(v reflect.Value, n *int, depth int, b bool) {
	*n++
	if depth > 4 {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Unix(int64(1_600_000_000+*n), 0).UTC()))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() || isSync(f.Type) {
				continue
			}
			fill(v.Field(i), n, depth+1, b)
		}
	case reflect.Pointer:
		if v.Type().Elem().Kind() != reflect.Struct || isSync(v.Type().Elem()) {
			return
		}
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), n, depth+1, b)
		v.Set(p)
	case reflect.String:
		v.SetString("v" + strconv.Itoa(*n))
	case reflect.Bool:
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n))
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), n, depth+1, b)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, n, depth+1, b)
		fill(e, n, depth+1, b)
		m.SetMapIndex(k, e)
		v.Set(m)
	default: // the other kinds are left zero
	}
}

func isSync(t reflect.Type) bool {
	p := t.PkgPath()
	return p == "sync" || p == "sync/atomic"
}

// Fields returns the names of the exported fields of the struct type t, with
// the fields of embedded structs promoted as Go promotes them. The struct
// pointer fields named in into are replaced by their own fields, as
// "Target.TargetStr". Fields of the sync packages are left out.
func Fields(t reflect.Type, into ...string) []string {
	var names []string
	for f := range t.Fields() {
		if isSync(f.Type) {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			names = append(names, Fields(f.Type)...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if f.Type.Kind() == reflect.Pointer && slices.Contains(into, f.Name) {
			for _, sub := range Fields(f.Type.Elem()) {
				names = append(names, f.Name+"."+sub)
			}
			continue
		}
		names = append(names, f.Name)
	}
	return names
}

// Value returns the field of the struct v that Fields names name.
func Value(v reflect.Value, name string) reflect.Value {
	for part := range strings.SplitSeq(name, ".") {
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		v = v.FieldByName(part)
	}
	return v
}
