package textutil

import "testing"

func TestParseBool(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"1", true, false},
		{"true", true, false},
		{"True", true, false},
		{"TRUE", true, false},
		{" true ", true, false},
		{"\tfalse", false, false},
		{"0", false, false},
		{"yes", false, true},
		{"", false, true},
	} {
		got, err := ParseBool(tc.in)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("ParseBool(%q) = %v, %v; want %v, error %v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}
