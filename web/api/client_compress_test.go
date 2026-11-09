package api

import "testing"

func TestRequestClientCompress(t *testing.T) {
	for _, c := range []struct {
		values map[string]string
		on     bool
		alg    string
	}{
		{map[string]string{}, false, ""},
		{map[string]string{"compress": "0"}, false, ""},
		{map[string]string{"compress": "1"}, true, ""},
		{map[string]string{"compress": "1", "compress_alg": "zstd"}, true, "zstd"},
		{map[string]string{"compress": "1", "compress_alg": "off"}, false, ""},
		{map[string]string{"compress": "flate"}, true, "flate"},
		{map[string]string{"compress": "0", "compress_alg": "zstd"}, false, ""},
	} {
		on, alg := requestClientCompress(formContext{values: c.values})
		if on != c.on || alg != c.alg {
			t.Errorf("%v: got %v %q want %v %q", c.values, on, alg, c.on, c.alg)
		}
	}
}
