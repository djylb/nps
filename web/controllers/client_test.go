package controllers

import (
	"reflect"
	"testing"
)

func TestRemoveRepeatedElement(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []string{}, want: nil},
		{name: "unique", in: []string{"1.1.1.1", "2.2.2.2"}, want: []string{"1.1.1.1", "2.2.2.2"}},
		{name: "duplicate keep last occurrence order", in: []string{"a", "b", "a", "c", "b"}, want: []string{"a", "c", "b"}},
		{name: "keep blank line once", in: []string{"", "", "10.0.0.1"}, want: []string{"", "10.0.0.1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveRepeatedElement(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RemoveRepeatedElement(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
