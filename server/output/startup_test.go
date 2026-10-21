package output

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

// The passphrase lives in its own file, so the outputs file can be rendered
// from configuration that is not secret.
func TestLoadStartupConfig_ReadsPassphraseFile(t *testing.T) {
	secret := writeFile(t, "passphrase", "correct horse battery\n")
	path := writeFile(t, "outputs.json", `{
	  "destinations": [
	    {"type": "srt-caller", "address": "203.0.113.5", "port": 9000,
	     "encryption": "aes-128", "passphraseFile": "`+secret+`"}
	  ]
	}`)

	cfg, err := LoadStartupConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Destinations, 1)
	require.Equal(t, "correct horse battery", cfg.Destinations[0].Passphrase)
	require.Equal(t, "203.0.113.5", cfg.Destinations[0].Address)
}

func TestLoadStartupConfig_Rejects(t *testing.T) {
	secret := writeFile(t, "passphrase", "x")
	for name, body := range map[string]string{
		// A misspelled field would otherwise start an output without it.
		"unknown field": `{"destinations": [{"type": "rtmp", "uri": "rtmp://h/a"}]}`,
		"two passphrases": `{"destinations": [{"type": "srt-caller", "address": "h", "port": 1,
			"passphrase": "a", "passphraseFile": "` + secret + `"}]}`,
		"missing passphrase file": `{"destinations": [{"type": "srt-caller", "address": "h", "port": 1,
			"passphraseFile": "/nonexistent/passphrase"}]}`,
		"relative passphrase file": `{"destinations": [{"type": "srt-caller", "address": "h", "port": 1,
			"passphraseFile": "passphrase"}]}`,
		"trailing value":           `{"destinations": []} {"recording": {"outputDir": "/r"}}`,
		"relative recording dir":   `{"recording": {"outputDir": "recordings"}}`,
		"traversing recording dir": `{"recording": {"outputDir": "/data/../etc"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadStartupConfig(writeFile(t, "outputs.json", body))
			require.Error(t, err)
		})
	}
}

func TestStartupRecording_Defaults(t *testing.T) {
	rc, err := StartupRecording{OutputDir: "/recordings/"}.RecorderConfig()
	require.NoError(t, err)
	require.Equal(t, "/recordings", rc.Dir)
	require.Equal(t, time.Hour, rc.RotateAfter)
	require.Zero(t, rc.MaxFileSize)

	rc, err = StartupRecording{OutputDir: "/r", RotateAfterMins: 5, MaxFileSizeMB: 2}.RecorderConfig()
	require.NoError(t, err)
	require.Equal(t, 5*time.Minute, rc.RotateAfter)
	require.Equal(t, int64(2*1024*1024), rc.MaxFileSize)
}

// Every declared destination is added and started, and the recording runs.
func TestManager_ApplyStartup(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	err := mgr.ApplyStartup(StartupConfig{
		Destinations: []StartupDestination{
			{DestinationConfig: DestinationConfig{Type: "srt-caller", Address: "192.0.2.1", Port: 9000}},
			{DestinationConfig: DestinationConfig{Type: "srt-caller", Address: "192.0.2.2", Port: 9001}},
		},
		Recording: &StartupRecording{OutputDir: t.TempDir()},
	})
	require.NoError(t, err)

	dests := mgr.ListDestinations()
	require.Len(t, dests, 2)
	for _, d := range dests {
		require.NotEqual(t, "stopped", d.State, "destination %s not started", d.Config.Address)
	}
	require.True(t, mgr.RecordingStatus().Active)
}

// A destination the manager refuses fails startup rather than being skipped.
func TestManager_ApplyStartup_StopsOnRefusedDestination(t *testing.T) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	err := mgr.ApplyStartup(StartupConfig{
		Destinations: []StartupDestination{
			{DestinationConfig: DestinationConfig{Type: "srt-caller", Port: 9000}},
		},
	})
	require.ErrorContains(t, err, "address is required")
}
