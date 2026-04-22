package htmlgfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DiscoverTemplates must build a valid file:// URL even when TemplatesDir
// is configured as a relative path. CEF rejects file://relative/... as
// ERR_INVALID_URL because it treats the first segment as a host.
func TestDiscoverTemplates_URLIsAbsoluteFileURI(t *testing.T) {
	abs := t.TempDir()
	tmplDir := filepath.Join(abs, "my-template")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}

	m := &Manager{config: Config{TemplatesDir: rel}}
	got := m.DiscoverTemplates()
	if len(got) != 1 {
		t.Fatalf("expected 1 template, got %d", len(got))
	}
	url := got[0].URL
	if !strings.HasPrefix(url, "file:///") {
		t.Fatalf("URL must start with file:/// (three slashes for empty host), got %q", url)
	}
	// The path component after file:// must be absolute (start with /).
	pathPart := strings.TrimPrefix(url, "file://")
	if !strings.HasPrefix(pathPart, "/") {
		t.Fatalf("path component must be absolute, got %q in URL %q", pathPart, url)
	}
}
