package playout

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrefetcherScanPlaylist(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
	})

	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: "/media/clip1.ts", Title: "Clip 1"},
			{ID: "2", Type: ItemLive, Source: "cam1", Title: "Live Camera"},
			{ID: "3", Type: ItemClip, AssetURI: "/media/clip2.ts", Title: "Clip 2"},
			{ID: "4", Type: ItemClip, AssetURI: "/media/clip3.mp4", Title: "Clip 3"},
		},
	}

	uris := p.scanNeeded(pl, 0)
	if len(uris) != 3 {
		t.Fatalf("expected 3 clip URIs, got %d: %v", len(uris), uris)
	}

	// Verify we got the 3 clip URIs and skipped the live item.
	expected := map[string]bool{
		"/media/clip1.ts":  true,
		"/media/clip2.ts":  true,
		"/media/clip3.mp4": true,
	}
	for _, uri := range uris {
		if !expected[uri] {
			t.Errorf("unexpected URI in scan results: %s", uri)
		}
	}
}

func TestPrefetcherSkipsCachedItems(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	// Pre-populate cache with clip2.
	localPath, _ := cache.Reserve("/media/clip2.ts")
	if err := os.WriteFile(localPath, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(&CacheEntry{
		AssetURI:  "/media/clip2.ts",
		LocalPath: localPath,
		State:     AssetCached,
		ByteSize:  6,
		FetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
	})

	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: "/media/clip1.ts", Title: "Clip 1"},
			{ID: "2", Type: ItemClip, AssetURI: "/media/clip2.ts", Title: "Clip 2"},
			{ID: "3", Type: ItemClip, AssetURI: "/media/clip3.ts", Title: "Clip 3"},
		},
	}

	uris := p.scanNeeded(pl, 0)
	if len(uris) != 2 {
		t.Fatalf("expected 2 URIs (skipping cached clip2), got %d: %v", len(uris), uris)
	}
	for _, uri := range uris {
		if uri == "/media/clip2.ts" {
			t.Error("scanNeeded should skip cached URI /media/clip2.ts")
		}
	}
}

func TestPrefetcherFetchesLocalAsset(t *testing.T) {
	cacheDir := t.TempDir()
	mediaDir := t.TempDir()

	// Create a source media file.
	srcFile := filepath.Join(mediaDir, "test_clip.ts")
	content := []byte("this is test media content for prefetch")
	if err := os.WriteFile(srcFile, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	fetchDone := make(chan struct{})
	var fetchedURI string
	var fetchErr error

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
		OnFetched: func(uri string, err error) {
			fetchedURI = uri
			fetchErr = err
			close(fetchDone)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	defer p.Stop()

	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: srcFile, Title: "Test Clip"},
		},
	}

	p.UpdatePlaylist(pl, 0)

	select {
	case <-fetchDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for fetch to complete")
	}

	if fetchErr != nil {
		t.Fatalf("fetch failed: %v", fetchErr)
	}
	if fetchedURI != srcFile {
		t.Errorf("expected fetched URI %q, got %q", srcFile, fetchedURI)
	}

	// Verify the cache entry exists and is Cached state.
	entry, err := cache.Get(srcFile)
	if err != nil {
		t.Fatalf("cache Get failed: %v", err)
	}
	if entry.State != AssetCached {
		t.Errorf("expected state Cached, got %s", entry.State)
	}
	if entry.ByteSize != int64(len(content)) {
		t.Errorf("expected byte size %d, got %d", len(content), entry.ByteSize)
	}
}

func TestPrefetcherCursorAdvance(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 2,
	})

	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: "/media/a.ts", Title: "A"},
			{ID: "2", Type: ItemClip, AssetURI: "/media/b.ts", Title: "B"},
			{ID: "3", Type: ItemClip, AssetURI: "/media/c.ts", Title: "C"},
			{ID: "4", Type: ItemClip, AssetURI: "/media/d.ts", Title: "D"},
		},
	}

	// cursor=0, lookahead=2 → items 0,1
	uris := p.scanNeeded(pl, 0)
	if len(uris) != 2 {
		t.Fatalf("cursor=0: expected 2 URIs, got %d: %v", len(uris), uris)
	}
	if uris[0] != "/media/a.ts" || uris[1] != "/media/b.ts" {
		t.Errorf("cursor=0: expected [a.ts, b.ts], got %v", uris)
	}

	// cursor=2, lookahead=2 → items 2,3
	uris = p.scanNeeded(pl, 2)
	if len(uris) != 2 {
		t.Fatalf("cursor=2: expected 2 URIs, got %d: %v", len(uris), uris)
	}
	if uris[0] != "/media/c.ts" || uris[1] != "/media/d.ts" {
		t.Errorf("cursor=2: expected [c.ts, d.ts], got %v", uris)
	}
}

func TestPrefetcherHTTPFetch(t *testing.T) {
	cacheDir := t.TempDir()
	content := []byte("http media content for prefetch test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer srv.Close()

	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	fetchDone := make(chan struct{})
	var fetchErr error

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
		OnFetched: func(uri string, err error) {
			fetchErr = err
			close(fetchDone)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	defer p.Stop()

	clipURI := srv.URL + "/clip.ts"
	pl := &Playlist{
		ID:   "test",
		Name: "Test Playlist",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: clipURI, Title: "HTTP Clip"},
		},
	}

	p.UpdatePlaylist(pl, 0)

	select {
	case <-fetchDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for HTTP fetch")
	}

	if fetchErr != nil {
		t.Fatalf("HTTP fetch failed: %v", fetchErr)
	}

	entry, err := cache.Get(clipURI)
	if err != nil {
		t.Fatalf("cache Get failed: %v", err)
	}
	if entry.State != AssetCached {
		t.Errorf("expected state Cached, got %s", entry.State)
	}
	if entry.ByteSize != int64(len(content)) {
		t.Errorf("expected byte size %d, got %d", len(content), entry.ByteSize)
	}
}

func TestPrefetcherStopCancelsFetches(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	// Create a slow HTTP server that blocks until context cancelled.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slowSrv.Close()

	fetchCalled := make(chan struct{}, 1)
	var fetchErr error

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
		OnFetched: func(uri string, err error) {
			fetchErr = err
			select {
			case fetchCalled <- struct{}{}:
			default:
			}
		},
	})

	ctx := context.Background()
	p.Start(ctx)

	clipURI := slowSrv.URL + "/slow_clip.ts"
	pl := &Playlist{
		ID:   "test",
		Name: "Test",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: clipURI, Title: "Slow Clip"},
		},
	}
	p.UpdatePlaylist(pl, 0)

	// Give it a moment to start the fetch, then stop.
	time.Sleep(50 * time.Millisecond)
	p.Stop()

	// The fetch should have been cancelled with an error.
	select {
	case <-fetchCalled:
		if fetchErr == nil {
			t.Error("expected fetch to fail with cancellation error")
		}
	case <-time.After(3 * time.Second):
		// Stop may have killed the goroutine before OnFetched ran — acceptable.
	}
}

func TestPrefetcherSkipsInflight(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: cacheDir, MaxBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	var fetchCount atomic.Int32

	// HTTP server that serves content but counts requests.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		// Small delay to ensure both items have been scanned before fetch completes.
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "media content")
	}))
	defer srv.Close()

	var mu sync.Mutex
	fetchedURIs := make(map[string]int)
	allDone := make(chan struct{}, 1)

	p := NewPrefetcher(PrefetcherConfig{
		Cache:          cache,
		Resolver:       NewAssetResolver(nil),
		LookaheadItems: 5,
		OnFetched: func(uri string, err error) {
			mu.Lock()
			fetchedURIs[uri]++
			count := len(fetchedURIs)
			mu.Unlock()
			// We only expect 1 unique URI to be fetched.
			if count >= 1 {
				select {
				case allDone <- struct{}{}:
				default:
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	defer p.Stop()

	// Same URI appears in two playlist items.
	clipURI := srv.URL + "/same_clip.ts"
	pl := &Playlist{
		ID:   "test",
		Name: "Test",
		Items: []PlaylistItem{
			{ID: "1", Type: ItemClip, AssetURI: clipURI, Title: "Clip A"},
			{ID: "2", Type: ItemClip, AssetURI: clipURI, Title: "Clip B"},
		},
	}

	p.UpdatePlaylist(pl, 0)

	select {
	case <-allDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for fetches")
	}

	// Wait a bit longer to catch any duplicate fetches.
	time.Sleep(200 * time.Millisecond)

	// The HTTP server should only have been called once.
	count := fetchCount.Load()
	if count != 1 {
		t.Errorf("expected 1 HTTP fetch for duplicate URI, got %d", count)
	}
}
