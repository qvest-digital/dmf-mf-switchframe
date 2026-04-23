package main

import (
	"reflect"
	"testing"
)

func TestReorderFlags(t *testing.T) {
	boolFlags := map[string]bool{"follow": true, "long": true, "json": true}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"no flags", []string{"BUG-1"}, []string{"BUG-1"}},
		{"flag after positional", []string{"BUG-1", "--intent", "hi"}, []string{"--intent", "hi", "BUG-1"}},
		{"equals form", []string{"BUG-1", "--intent=hi"}, []string{"--intent=hi", "BUG-1"}},
		{"bool after positional", []string{"BUG-1", "--long"}, []string{"--long", "BUG-1"}},
		{"multi positional+flags", []string{"BUG-1", "50", "--note", "done"}, []string{"--note", "done", "BUG-1", "50"}},
		{"already-front", []string{"--intent", "hi", "BUG-1"}, []string{"--intent", "hi", "BUG-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reorderFlags(tc.in, boolFlags)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
