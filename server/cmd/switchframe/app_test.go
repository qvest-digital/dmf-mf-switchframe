package main

import (
	"testing"
)

func TestTokenPrefixShortToken(t *testing.T) {
	// A user-provided API token shorter than 8 characters must not panic.
	short := "ab"
	got := tokenPrefix(short)
	if got == "" {
		t.Fatal("expected non-empty prefix for short token")
	}
	if len(got) > len(short)+3 { // at most "ab..."
		t.Fatalf("prefix too long: %q", got)
	}
}

func TestTokenPrefixNormalToken(t *testing.T) {
	tok := "abcdef1234567890"
	got := tokenPrefix(tok)
	expected := "abcdef12..."
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestTokenPrefixEmptyToken(t *testing.T) {
	got := tokenPrefix("")
	if got != "***" {
		t.Fatalf("expected %q for empty token, got %q", "***", got)
	}
}
