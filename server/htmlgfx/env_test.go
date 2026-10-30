package htmlgfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The sidecar renders operator-supplied pages in an unsandboxed Chromium, so
// the engine's secrets must not reach its environment. A stand-in binary
// records what it was spawned with.
func TestStart_SidecarEnvironmentIsAllowlisted(t *testing.T) {
	t.Setenv("SWITCHFRAME_API_TOKEN", "fake-token")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("HOME", "/home/fake")

	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	bin := filepath.Join(dir, "fake-sidecar")
	script := "#!/bin/sh\nenv > '" + out + "'\necho READY\nsleep 5\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(nil, Config{BinaryPath: bin, PoolSize: 1})
	// The stand-in has no control socket, so Start reports a connect error
	// after the process has run.
	_ = m.Start()
	t.Cleanup(func() { _ = m.Close() })

	var got string
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			got = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got == "" {
		t.Fatal("stand-in sidecar did not record its environment")
	}

	allowed := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true, "TZ": true,
		"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "PWD": true, "SHLVL": true, "_": true, "OLDPWD": true}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		name, _, _ := strings.Cut(line, "=")
		if !allowed[name] && !strings.HasPrefix(name, "LC_") {
			t.Errorf("sidecar inherited %q", name)
		}
	}
	if !strings.Contains(got, "HOME=/home/fake") || !strings.Contains(got, "LC_ALL=C.UTF-8") {
		t.Errorf("allowlisted variables were dropped:\n%s", got)
	}
}
