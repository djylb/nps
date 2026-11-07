// Package fieldtest helps tests check that code handling every field of a
// struct keeps up with fields added to it.
package fieldtest

import (
	"reflect"
	"strconv"
	"time"
)

// Fill sets every exported field of the struct v points to, and of the
// structs its pointer fields lead to, to a value that differs from the zero
// value and from the other fields. Fields of the sync packages are left alone.
func Fill(v any) {
	n := 0
	fill(reflect.ValueOf(v).Elem(), &n, 0)
}

var timeType = reflect.TypeFor[time.Time]()

func fill(v reflect.Value, n *int, depth int) {
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
			fill(v.Field(i), n, depth+1)
		}
	case reflect.Pointer:
		if v.Type().Elem().Kind() != reflect.Struct || isSync(v.Type().Elem()) {
			return
		}
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), n, depth+1)
		v.Set(p)
	case reflect.String:
		v.SetString("v" + strconv.Itoa(*n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n))
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), n, depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, n, depth+1)
		fill(e, n, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	}
}

func isSync(t reflect.Type) bool {
	p := t.PkgPath()
	return p == "sync" || p == "sync/atomic"
}

// Fields returns the names of the exported fields of the struct type t, with
// the fields of embedded structs promoted as Go promotes them. Fields of the
// sync packages are left out.
func Fields(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if isSync(f.Type) {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			names = append(names, Fields(f.Type)...)
			continue
		}
		if f.IsExported() {
			names = append(names, f.Name)
		}
	}
	return names
}
