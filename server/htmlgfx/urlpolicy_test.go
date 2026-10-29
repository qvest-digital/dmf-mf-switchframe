package htmlgfx

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCheckURL(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "templates")
	if err := os.MkdirAll(filepath.Join(dir, "lower-third"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, "lower-third", "index.html")
	secret := filepath.Join(base, "secret")
	for _, f := range []string{index, secret} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A link inside the directory pointing out of it.
	if err := os.Symlink(secret, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	// A sibling whose name starts with the directory's.
	if err := os.MkdirAll(dir+"-other", 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(dir+"-other", "index.html")
	if err := os.WriteFile(sibling, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		url     string
		allowed bool
	}{
		{"https://graphics.example.com/lower-third/index.html", true},
		{"HTTPS://graphics.example.com/a.html", true},
		{"file://" + index, true},
		{"file://localhost" + index, true},
		{"file://" + index + "?manifest=https://graphics.example.com/x.ograf.json", true},

		{"http://graphics.example.com/a.html", false},
		{"http://10.0.0.1:9090/metrics", false},
		{"https:///a.html", false},
		{"file://" + secret, false},
		{"file:///etc/passwd", false},
		{"file:///var/run/secrets/kubernetes.io/serviceaccount/token", false},
		{"file://" + dir + "/../secret", false},
		{"file://" + dir + "/%2e%2e/secret", false},
		{"file://" + filepath.Join(dir, "escape.html"), false},
		{"file://" + sibling, false},
		{"file://" + dir, false},
		{"file://" + filepath.Join(dir, "missing.html"), false},
		{"file://otherhost" + index, false},
		{"javascript:alert(1)", false},
		{"data:text/html,<p>x</p>", false},
		{"chrome://settings", false},
		{"about:blank", false},
		{"lower-third/index.html", false},
		{"", false},
	} {
		err := CheckURL(tc.url, dir)
		if tc.allowed && err != nil {
			t.Errorf("%q: refused: %v", tc.url, err)
		}
		if !tc.allowed && !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("%q: got %v, want ErrURLNotAllowed", tc.url, err)
		}
	}
}

// Without a templates directory no file URL is under it.
func TestCheckURL_NoTemplatesDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckURL("file://"+f, ""); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("got %v, want ErrURLNotAllowed", err)
	}
	if err := CheckURL("https://graphics.example.com/a.html", ""); err != nil {
		t.Errorf("https refused: %v", err)
	}
}

// The sidecar enforces the same file URL boundary on every navigation and
// resource load, so it has to be told the directory, and as an absolute path:
// it does not resolve it against the engine's working directory.
func TestSidecarArgs_TemplatesDir(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	args := NewManager(nil, Config{TemplatesDir: "templates"}).sidecarArgs("/run/sock")
	want := []string{
		"--control-socket", "/run/sock/control.sock",
		"--frame-socket", "/run/sock/frames.sock",
		"--user-data-dir", "/run/sock/cef-data",
		"--templates-dir", filepath.Join(wd, "templates"),
	}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}

	if args := NewManager(nil, Config{}).sidecarArgs("/run/sock"); slices.Contains(args, "--templates-dir") {
		t.Errorf("no templates directory configured, args = %q", args)
	}
}
