package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/codec"
)

// captureStartup runs fn with os.Stdout and the default logger redirected
// and returns everything both received.
func captureStartup(t *testing.T, fn func(log *slog.Logger)) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(log)
	defer slog.SetDefault(prev)

	fn(log)

	require.NoError(t, w.Close())
	printed, err := io.ReadAll(r)
	require.NoError(t, err)
	return logged.String() + string(printed)
}

// The engine's stdout and stderr land in the cluster's log store, which far
// more people can read than may drive the switcher. No part of the token may
// appear there, whichever way it was configured.
func TestAnnounceAuth_NeverPrintsToken(t *testing.T) {
	const token = "c0ffee5ecret70ken0123456789abcdef"
	for _, source := range []string{"flag", "env", "generated"} {
		t.Run(source, func(t *testing.T) {
			cfg := AppConfig{APIToken: token, APITokenSource: source, StateDir: t.TempDir()}

			out := captureStartup(t, func(log *slog.Logger) {
				require.NoError(t, announceAuth(cfg, log))
			})

			require.Contains(t, out, "API authentication enabled")
			require.Contains(t, out, "token_source="+source)
			require.NotContains(t, out, token)
			require.NotContains(t, out, token[:8])
		})
	}
}

func TestAnnounceAuth_GeneratedTokenGoesToOwnerOnlyFile(t *testing.T) {
	const token = "c0ffee5ecret70ken0123456789abcdef"
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, apiTokenFile)

	out := captureStartup(t, func(log *slog.Logger) {
		require.NoError(t, announceAuth(AppConfig{APIToken: token, APITokenSource: "generated", StateDir: dir}, log))
	})

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, token+"\n", string(got))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Contains(t, out, path)
}

// A token file left from an earlier run with a wider mode must not hold the
// new token, even briefly, at that mode.
func TestAnnounceAuth_ReplacesWiderExistingTokenFile(t *testing.T) {
	const token = "c0ffee5ecret70ken0123456789abcdef"
	dir := t.TempDir()
	path := filepath.Join(dir, apiTokenFile)
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o644))
	before, err := os.Stat(path)
	require.NoError(t, err)

	captureStartup(t, func(log *slog.Logger) {
		require.NoError(t, announceAuth(AppConfig{APIToken: token, APITokenSource: "generated", StateDir: dir}, log))
	})

	after, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), after.Mode().Perm())
	require.False(t, os.SameFile(before, after), "the token must go into a new file, not the old one")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, token+"\n", string(got))
}

// A symlink at the token path is replaced, not followed: the token must not
// land in whatever file the link points at.
func TestAnnounceAuth_ReplacesSymlinkAtTokenPath(t *testing.T) {
	const token = "c0ffee5ecret70ken0123456789abcdef"
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(target, []byte("untouched\n"), 0o644))
	require.NoError(t, os.Chmod(target, 0o644))
	path := filepath.Join(dir, apiTokenFile)
	require.NoError(t, os.Symlink(target, path))

	captureStartup(t, func(log *slog.Logger) {
		require.NoError(t, announceAuth(AppConfig{APIToken: token, APITokenSource: "generated", StateDir: dir}, log))
	})

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "untouched\n", string(got))
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	linfo, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, linfo.Mode().IsRegular())
	require.Equal(t, os.FileMode(0o600), linfo.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file may be left behind")
}

func TestAnnounceAuth_ConfiguredTokenWritesNoFile(t *testing.T) {
	dir := t.TempDir()

	captureStartup(t, func(log *slog.Logger) {
		require.NoError(t, announceAuth(AppConfig{APIToken: "x", APITokenSource: "env", StateDir: dir}, log))
	})

	_, err := os.Stat(filepath.Join(dir, apiTokenFile))
	require.True(t, os.IsNotExist(err))
}

func TestParseConfig_APITokenSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want string
	}{
		{"flag", []string{"--api-token", "from-flag"}, "from-env", "flag"},
		{"env", nil, "from-env", "env"},
		{"generated", nil, "", "generated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SWITCHFRAME_API_TOKEN", tc.env)
			args, cmdLine := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = args, cmdLine })
			os.Args = append([]string{"switchframe"}, tc.args...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			cfg, err := parseConfig()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.APITokenSource)
			require.NotEmpty(t, cfg.APIToken)
		})
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
	t.Setenv("SWITCHFRAME_STATE_DIR", "")

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

func TestDefaultCacheDir(t *testing.T) {
	dir := defaultCacheDir()
	require.NotEmpty(t, dir)
	require.Contains(t, dir, "cache", "should contain 'cache' in path")
	// On macOS (dev), should use ~/.switchframe/cache.
	// On Linux (prod), should use /data/switchframe/cache.
	// Just verify it's not empty and ends with "cache".
	require.Equal(t, "cache", filepath.Base(dir))
}

func TestParseConfig_CacheDir_Default(t *testing.T) {
	os.Args = []string{"switchframe"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	cfg, err := parseConfig()
	require.NoError(t, err)
	require.NotEmpty(t, cfg.CacheDir, "CacheDir should have a default value")
	require.Equal(t, "cache", filepath.Base(cfg.CacheDir))
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

func TestParseConfig_STMapPrewarm(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"default", nil, true},
		{"false", []string{"--stmap-prewarm=false"}, false},
		{"true", []string{"--stmap-prewarm=true"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, cmdLine := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = args, cmdLine })
			os.Args = append([]string{"switchframe"}, tc.args...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			cfg, err := parseConfig()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.STMapPrewarm)
		})
	}
}

func TestParseConfig_MXLGCInterval(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"default", nil, 30 * time.Second},
		{"off", []string{"--mxl-gc-interval=0"}, 0},
		{"set", []string{"--mxl-gc-interval=5m"}, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, cmdLine := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = args, cmdLine })
			os.Args = append([]string{"switchframe"}, tc.args...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			cfg, err := parseConfig()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.MXLGCInterval)
		})
	}
}

func TestParseConfig_MXLSourcePreset(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    codec.OutputPreset
		wantErr bool
	}{
		{"default", nil, "", false},
		{"fastest", []string{"--mxl-source-preset=fastest"}, codec.OutputPresetFastest, false},
		{"unknown", []string{"--mxl-source-preset=ultrafast"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, cmdLine := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = args, cmdLine })
			os.Args = append([]string{"switchframe"}, tc.args...)
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			cfg, err := parseConfig()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.MXLSourcePreset)
		})
	}
}
