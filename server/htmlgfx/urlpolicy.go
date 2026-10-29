package htmlgfx

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ErrURLNotAllowed is returned for a URL the sidecar must not load.
var ErrURLNotAllowed = errors.New("url not allowed")

// CheckURL reports whether rawURL may be loaded into a sidecar browser of
// this manager, by the package-level CheckURL.
func (m *Manager) CheckURL(rawURL string) error {
	return CheckURL(rawURL, m.config.TemplatesDir)
}

// CheckURL reports whether rawURL may be loaded into a sidecar browser that
// serves templates from templatesDir.
//
// The sidecar is an unsandboxed Chromium whose pixels go to program, so a
// URL is as good as reading whatever it names onto air. https is allowed;
// file is allowed only below the templates directory, so a mounted secret or
// any other file in the container cannot be loaded. Everything else is
// refused, plain http included: it reaches cluster-internal services with no
// certificate naming them.
func CheckURL(rawURL, templatesDir string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLNotAllowed, err)
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return fmt.Errorf("%w: https URL has no host", ErrURLNotAllowed)
		}
		return nil
	case "file":
		return checkFileURL(u, templatesDir)
	default:
		return fmt.Errorf("%w: scheme %q, want https or a file below the templates directory", ErrURLNotAllowed, u.Scheme)
	}
}

func checkFileURL(u *url.URL, templatesDir string) error {
	if u.Host != "" && u.Host != "localhost" {
		return fmt.Errorf("%w: file URL names host %q", ErrURLNotAllowed, u.Host)
	}
	if templatesDir == "" {
		return fmt.Errorf("%w: no templates directory configured for file URLs", ErrURLNotAllowed)
	}
	// Symlinks resolved on both sides, so a link inside the directory cannot
	// point out of it and a linked directory still matches itself.
	root, err := filepath.EvalSymlinks(templatesDir)
	if err != nil {
		return fmt.Errorf("%w: templates directory: %v", ErrURLNotAllowed, err)
	}
	if !filepath.IsAbs(u.Path) {
		return fmt.Errorf("%w: file URL path %q is not absolute", ErrURLNotAllowed, u.Path)
	}
	target, err := filepath.EvalSymlinks(filepath.Clean(u.Path))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLNotAllowed, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("%w: templates directory: %v", ErrURLNotAllowed, err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s is not below the templates directory", ErrURLNotAllowed, u.Path)
	}
	return nil
}
