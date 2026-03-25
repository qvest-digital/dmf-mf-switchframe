package playout

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AssetFetcher downloads or copies an asset from a source to a local path.
type AssetFetcher interface {
	Fetch(ctx context.Context, source string, destPath string) (bytesWritten int64, err error)
}

// LocalFetcher copies files from local or NFS paths.
type LocalFetcher struct {
	// AllowedPrefixes restricts which source paths are permitted.
	// If non-empty, the source path must start with one of these prefixes.
	// If empty, all paths are allowed.
	AllowedPrefixes []string
}

// Fetch copies a local file from source to destPath.
func (f *LocalFetcher) Fetch(ctx context.Context, source string, destPath string) (int64, error) {
	// Check context before starting.
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("asset fetch cancelled: %w", err)
	}

	// Validate allowed prefixes if configured.
	if len(f.AllowedPrefixes) > 0 {
		allowed := false
		for _, prefix := range f.AllowedPrefixes {
			if strings.HasPrefix(source, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return 0, ErrPathTraversal
		}
	}

	src, err := os.Open(source)
	if err != nil {
		return 0, fmt.Errorf("open source %q: %w", source, err)
	}
	defer src.Close()

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
	defer resp.Body.Close()

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
