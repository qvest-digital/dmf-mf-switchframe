package playout

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// AssetFetcher downloads or copies an asset from a source to a local path.
type AssetFetcher interface {
	Fetch(ctx context.Context, source string, destPath string) (bytesWritten int64, err error)
}

// LocalFetcher copies files from local or NFS paths.
type LocalFetcher struct {
	// AllowedPrefixes is a deny-by-default allow-list of absolute path prefixes
	// the fetcher may read from. The source path is resolved with filepath.Abs
	// + Clean and must be inside one of these prefixes (compared with a trailing
	// separator so /var/foo does not match /var/foo-other). An empty or nil
	// slice denies every path.
	AllowedPrefixes []string
}

// Fetch copies a local file from source to destPath.
func (f *LocalFetcher) Fetch(ctx context.Context, source string, destPath string) (int64, error) {
	// Check context before starting.
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("asset fetch cancelled: %w", err)
	}

	if err := IsLocalPathAllowed(source, f.AllowedPrefixes); err != nil {
		slog.Warn("playout: local asset fetch denied",
			"source", source, "allowed_prefixes", f.AllowedPrefixes, "error", err)
		return 0, err
	}

	src, err := os.Open(source)
	if err != nil {
		return 0, fmt.Errorf("open source %q: %w", source, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create dest %q: %w", destPath, err)
	}

	n, err := io.Copy(dst, src)
	if err != nil {
		_ = dst.Close()
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("copy to %q: %w", destPath, err)
	}

	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("fsync %q: %w", destPath, err)
	}

	if err := dst.Close(); err != nil {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("close %q: %w", destPath, err)
	}

	return n, nil
}

// HTTPFetcher downloads files from HTTP/HTTPS URLs.
type HTTPFetcher struct {
	// Client is the HTTP client to use. If nil, http.DefaultClient is used.
	Client *http.Client
}

func (f *HTTPFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

// Fetch downloads the resource at source (a URL) to destPath.
func (f *HTTPFetcher) Fetch(ctx context.Context, source string, destPath string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return 0, fmt.Errorf("create request for %q: %w", source, err)
	}

	resp, err := f.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch %q: %w", source, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch %q returned status %d: %w", source, resp.StatusCode, ErrFetchFailed)
	}

	dst, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("create dest %q: %w", destPath, err)
	}

	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		_ = dst.Close()
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("write to %q: %w", destPath, err)
	}

	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("fsync %q: %w", destPath, err)
	}

	if err := dst.Close(); err != nil {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("close %q: %w", destPath, err)
	}

	return n, nil
}

// AssetResolver maps asset URIs to the appropriate fetcher.
type AssetResolver struct {
	localFetcher *LocalFetcher
	httpFetcher  *HTTPFetcher
}

// NewAssetResolver creates a resolver with LocalFetcher and HTTPFetcher.
// allowedLocalPrefixes restricts which local paths the LocalFetcher may read.
func NewAssetResolver(allowedLocalPrefixes []string) *AssetResolver {
	return &AssetResolver{
		localFetcher: &LocalFetcher{AllowedPrefixes: allowedLocalPrefixes},
		httpFetcher:  &HTTPFetcher{},
	}
}

// FetcherFor returns the appropriate fetcher for the given asset URI.
// Returns ErrUnsupportedURI for schemes that are not yet implemented (e.g., s3).
func (r *AssetResolver) FetcherFor(uri string) (AssetFetcher, error) {
	scheme, _ := ParseAssetURI(uri)
	switch scheme {
	case "local":
		return r.localFetcher, nil
	case "http", "https":
		return r.httpFetcher, nil
	default:
		return nil, fmt.Errorf("scheme %q: %w", scheme, ErrUnsupportedURI)
	}
}

// IsLocalPathAllowed returns nil if path resolves (after filepath.Abs +
// Clean) inside one of the allowed prefixes. The check uses a trailing
// separator on both sides so a configured prefix /var/foo does not match
// /var/foo-bar. A nil or empty allowedPrefixes slice rejects every path
// (deny by default).
func IsLocalPathAllowed(path string, allowedPrefixes []string) error {
	if len(allowedPrefixes) == 0 {
		return ErrPathTraversal
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	abs = filepath.Clean(abs)
	sep := string(os.PathSeparator)
	for _, prefix := range allowedPrefixes {
		if prefix == "" {
			continue
		}
		cleanPrefix := filepath.Clean(prefix)
		if !filepath.IsAbs(cleanPrefix) {
			// Refuse to compare against a relative prefix — operator
			// configuration mistake.
			continue
		}
		// Root prefix matches every absolute path.
		if cleanPrefix == sep {
			return nil
		}
		// Either the path equals the prefix exactly, or it's strictly inside
		// the prefix directory. The trailing-separator form prevents
		// /var/foo from matching /var/foo-bar.
		if abs == cleanPrefix || strings.HasPrefix(abs, cleanPrefix+sep) {
			return nil
		}
	}
	return ErrPathTraversal
}

// SourcePath returns the source-specific path for the given asset URI.
// For local URIs, this is the file path. For HTTP/HTTPS, this is the full URL.
// For other schemes, the original URI is returned.
func (r *AssetResolver) SourcePath(uri string) string {
	scheme, path := ParseAssetURI(uri)
	switch scheme {
	case "local":
		return path
	case "http", "https":
		// Return the original URI as-is since it's already a valid URL.
		return uri
	default:
		return uri
	}
}
