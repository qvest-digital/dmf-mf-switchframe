package main

import "testing"

func TestVersion(t *testing.T) {
	got := version()
	if got == "" {
		t.Fatal("version() returned empty string")
	}
}
