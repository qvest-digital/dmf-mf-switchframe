package playout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFetcherCopiesFile(t *testing.T) {
	dir := t.TempDir()

	// Create source file with known content.
	srcPath := filepath.Join(dir, "source.ts")
	content := []byte("MPEG-TS test payload here")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	destPath := filepath.Join(dir, "dest.ts")

	f := &LocalFetcher{}
	n, err := f.Fetch(context.Background(), srcPath, destPath)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("bytes written = %d, want %d", n, len(content))
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("dest content = %q, want %q", got, content)
	}
}

func TestLocalFetcherFileNotFound(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "dest.ts")

	f := &LocalFetcher{}
	_, err := f.Fetch(context.Background(), "/no/such/file.ts", destPath)
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestLocalFetcherPathTraversal(t *testing.T) {
	f := &LocalFetcher{
		AllowedPrefixes: []string{"/mnt/media"},
	}

	_, err := f.Fetch(context.Background(), "/etc/passwd", "/tmp/dest")
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestLocalFetcherPathTraversalAllows(t *testing.T) {
	dir := t.TempDir()

	// Use dir as the allowed prefix and create a source under it.
	srcPath := filepath.Join(dir, "allowed.ts")
	if err := os.WriteFile(srcPath, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(dir, "dest.ts")

	f := &LocalFetcher{
		AllowedPrefixes: []string{dir},
	}
	_, err := f.Fetch(context.Background(), srcPath, destPath)
	if err != nil {
		t.Fatalf("Fetch should succeed for allowed prefix, got: %v", err)
	}
}

func TestLocalFetcherNoAllowedPrefixes(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "any.ts")
	if err := os.WriteFile(srcPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(dir, "dest.ts")

	// Empty AllowedPrefixes means no restriction.
	f := &LocalFetcher{}
	_, err := f.Fetch(context.Background(), srcPath, destPath)
	if err != nil {
		t.Fatalf("Fetch with no AllowedPrefixes should succeed, got: %v", err)
	}
}

func TestLocalFetcherContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	f := &LocalFetcher{}
	_, err := f.Fetch(ctx, "/some/file.ts", "/tmp/dest.ts")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestHTTPFetcherDownloadsFile(t *testing.T) {
	content := []byte("HTTP downloaded content here")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "downloaded.ts")

	f := &HTTPFetcher{}
	n, err := f.Fetch(context.Background(), srv.URL+"/test.ts", destPath)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("bytes written = %d, want %d", n, len(content))
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("dest content = %q, want %q", got, content)
	}
}

func TestHTTPFetcherCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve a huge response so the fetch won't complete instantly.
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 1000000; i++ {
			_, _ = io.WriteString(w, "data data data data\n")
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	dir := t.TempDir()
	destPath := filepath.Join(dir, "cancelled.ts")

	f := &HTTPFetcher{}
	_, err := f.Fetch(ctx, srv.URL+"/big.ts", destPath)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestHTTPFetcherBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "not found")
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "missing.ts")

	f := &HTTPFetcher{}
	_, err := f.Fetch(context.Background(), srv.URL+"/missing.ts", destPath)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("expected ErrFetchFailed, got %v", err)
	}
}

func TestHTTPFetcherCustomClient(t *testing.T) {
	content := []byte("custom client content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "custom.ts")

	f := &HTTPFetcher{Client: srv.Client()}
	n, err := f.Fetch(context.Background(), srv.URL+"/test.ts", destPath)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("bytes written = %d, want %d", n, len(content))
	}
}

func TestResolveAssetFetcher(t *testing.T) {
	resolver := NewAssetResolver(nil)

	tests := []struct {
		uri      string
		wantType string
	}{
		{"/mnt/media/clip.ts", "*playout.LocalFetcher"},
		{"file:///mnt/media/clip.ts", "*playout.LocalFetcher"},
		{"local:///mnt/media/clip.ts", "*playout.LocalFetcher"},
		{"http://cdn.example.com/clip.ts", "*playout.HTTPFetcher"},
		{"https://cdn.example.com/clip.ts", "*playout.HTTPFetcher"},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			fetcher, err := resolver.FetcherFor(tt.uri)
			if err != nil {
				t.Fatalf("FetcherFor(%q) returned error: %v", tt.uri, err)
			}
			got := fmt.Sprintf("%T", fetcher)
			if got != tt.wantType {
				t.Fatalf("FetcherFor(%q) type = %s, want %s", tt.uri, got, tt.wantType)
			}
		})
	}
}

func TestResolveAssetFetcherUnsupported(t *testing.T) {
	resolver := NewAssetResolver(nil)

	_, err := resolver.FetcherFor("s3://bucket/clip.ts")
	if !errors.Is(err, ErrUnsupportedURI) {
		t.Fatalf("expected ErrUnsupportedURI, got %v", err)
	}
}

func TestResolveAssetFetcherUnknownScheme(t *testing.T) {
	resolver := NewAssetResolver(nil)

	_, err := resolver.FetcherFor("ftp://example.com/clip.ts")
	if !errors.Is(err, ErrUnsupportedURI) {
		t.Fatalf("expected ErrUnsupportedURI for ftp://, got %v", err)
	}
}

func TestAssetResolverSourcePath(t *testing.T) {
	resolver := NewAssetResolver(nil)

	tests := []struct {
		uri  string
		want string
	}{
		{"/mnt/media/clip.ts", "/mnt/media/clip.ts"},
		{"file:///mnt/media/clip.ts", "/mnt/media/clip.ts"},
		{"local:///mnt/media/clip.ts", "/mnt/media/clip.ts"},
		{"http://cdn.example.com/clip.ts", "http://cdn.example.com/clip.ts"},
		{"https://cdn.example.com/clip.ts", "https://cdn.example.com/clip.ts"},
		{"s3://bucket/key/clip.ts", "s3://bucket/key/clip.ts"},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got := resolver.SourcePath(tt.uri)
			if got != tt.want {
				t.Fatalf("SourcePath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}
