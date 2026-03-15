package main

import (
	"testing"

	"github.com/stretchr/testify/require"
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

func TestValidateSCTE35PID(t *testing.T) {
	tests := []struct {
		name    string
		pid     int
		wantErr bool
	}{
		{"valid default", 0x102, false},
		{"valid min", 0x20, false},
		{"valid max", 0x1FFE, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too low", 0x1F, true},
		{"too large", 0x2000, true},
		{"overflow", 70000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSCTE35PID(tt.pid)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
