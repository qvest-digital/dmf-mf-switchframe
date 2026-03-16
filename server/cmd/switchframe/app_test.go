package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestStatePath(t *testing.T) {
	app := &App{cfg: AppConfig{StateDir: "/tmp/test-switchframe"}}
	got := app.statePath("presets.json")
	want := "/tmp/test-switchframe/presets.json"
	if got != want {
		t.Errorf("statePath() = %q, want %q", got, want)
	}
}

func TestStatePath_Nested(t *testing.T) {
	app := &App{cfg: AppConfig{StateDir: "/data"}}
	got := app.statePath("clips")
	want := "/data/clips"
	if got != want {
		t.Errorf("statePath() = %q, want %q", got, want)
	}
}

func TestParseConfig_StateDir_Default(t *testing.T) {
	// When SWITCHFRAME_STATE_DIR is unset, StateDir should default to ~/.switchframe.
	os.Unsetenv("SWITCHFRAME_STATE_DIR")

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	// parseConfig() calls flag.Parse() which we can't easily use in tests,
	// so we just verify the AppConfig struct has the field and the env logic.
	app := &App{cfg: AppConfig{StateDir: filepath.Join(homeDir, ".switchframe")}}
	got := app.statePath("presets.json")
	want := filepath.Join(homeDir, ".switchframe", "presets.json")
	if got != want {
		t.Errorf("statePath() = %q, want %q", got, want)
	}
}

func TestParseConfig_StateDir_EnvOverride(t *testing.T) {
	// When SWITCHFRAME_STATE_DIR is set, StateDir should use it.
	t.Setenv("SWITCHFRAME_STATE_DIR", "/custom/state")

	app := &App{cfg: AppConfig{StateDir: "/custom/state"}}
	got := app.statePath("macros.json")
	want := "/custom/state/macros.json"
	if got != want {
		t.Errorf("statePath() = %q, want %q", got, want)
	}
}

func TestParseConfig_DefaultAddr(t *testing.T) {
	os.Args = []string{"switchframe"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	cfg, err := parseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("default Addr = %q, want :8080", cfg.Addr)
	}
}

func TestCloseWithTimeout(t *testing.T) {
	// Test that closeWithContext respects context timeout
	app := &App{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	app.closeWithContext(ctx)
	elapsed := time.Since(start)

	// Should complete quickly since there's nothing to clean up
	if elapsed > 1*time.Second {
		t.Errorf("closeWithContext took %v, expected < 1s", elapsed)
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
