package main

import "testing"

func TestValidateEngineLabel_AcceptsEmptyAndAB(t *testing.T) {
	for _, v := range []string{"", "a", "b"} {
		if err := validateEngineLabel(v); err != nil {
			t.Errorf("validateEngineLabel(%q) = %v, want nil", v, err)
		}
	}
}

func TestValidateEngineLabel_RejectsInvalid(t *testing.T) {
	for _, v := range []string{"c", "A", "B", "1", "primary", " ", "ab"} {
		if err := validateEngineLabel(v); err == nil {
			t.Errorf("validateEngineLabel(%q) = nil, want error", v)
		}
	}
}
