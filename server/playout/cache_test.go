package playout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheNewCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent", "cache")
	cfg := CacheConfig{
		Dir:      dir,
		MaxBytes: 1 << 20, // 1 MB
	}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("cache dir is not a directory")
	}
}

func TestCachePutAndGet(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	// Reserve a path and create a dummy file.
	localPath, err := c.Reserve("s3://bucket/clip1.ts")
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := os.WriteFile(localPath, make([]byte, 500), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	now := time.Now()
	entry := &CacheEntry{
		AssetURI:     "s3://bucket/clip1.ts",
		LocalPath:    localPath,
		State:        AssetCached,
		ByteSize:     500,
		Checksum:     "abc123",
		LastAccessed: now,
		FetchedAt:    now,
	}

	if err := c.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := c.Get("s3://bucket/clip1.ts")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AssetURI != "s3://bucket/clip1.ts" {
		t.Errorf("AssetURI = %q, want %q", got.AssetURI, "s3://bucket/clip1.ts")
	}
	if got.LocalPath != localPath {
		t.Errorf("LocalPath = %q, want %q", got.LocalPath, localPath)
	}
	if got.ByteSize != 500 {
		t.Errorf("ByteSize = %d, want 500", got.ByteSize)
	}
	if got.Checksum != "abc123" {
		t.Errorf("Checksum = %q, want %q", got.Checksum, "abc123")
	}
	if got.State != AssetCached {
		t.Errorf("State = %v, want %v", got.State, AssetCached)
	}
}

func TestCacheGetNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	_, err = c.Get("s3://bucket/nonexistent.ts")
	if err != ErrAssetNotFound {
		t.Errorf("Get = %v, want ErrAssetNotFound", err)
	}
}

func TestCacheLRUEviction(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 100}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	// Add a 60-byte item A.
	pathA, _ := c.Reserve("asset://A")
	if err := os.WriteFile(pathA, make([]byte, 60), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	entryA := &CacheEntry{
		AssetURI:     "asset://A",
		LocalPath:    pathA,
		State:        AssetCached,
		ByteSize:     60,
		LastAccessed: time.Now().Add(-2 * time.Second),
	}
	if err := c.Put(entryA); err != nil {
		t.Fatalf("Put A: %v", err)
	}

	// Add a 60-byte item B — should evict A.
	pathB, _ := c.Reserve("asset://B")
	if err := os.WriteFile(pathB, make([]byte, 60), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}
	entryB := &CacheEntry{
		AssetURI:     "asset://B",
		LocalPath:    pathB,
		State:        AssetCached,
		ByteSize:     60,
		LastAccessed: time.Now(),
	}
	if err := c.Put(entryB); err != nil {
		t.Fatalf("Put B: %v", err)
	}

	// A should be evicted.
	_, err = c.Get("asset://A")
	if err != ErrAssetNotFound {
		t.Errorf("Get A = %v, want ErrAssetNotFound (should be evicted)", err)
	}

	// B should still be present.
	got, err := c.Get("asset://B")
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if got.ByteSize != 60 {
		t.Errorf("B ByteSize = %d, want 60", got.ByteSize)
	}
}

func TestCacheEvictionDeletesFile(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 100}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	pathA, _ := c.Reserve("asset://A")
	if err := os.WriteFile(pathA, make([]byte, 60), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	entryA := &CacheEntry{
		AssetURI:     "asset://A",
		LocalPath:    pathA,
		State:        AssetCached,
		ByteSize:     60,
		LastAccessed: time.Now().Add(-2 * time.Second),
	}
	if err := c.Put(entryA); err != nil {
		t.Fatalf("Put A: %v", err)
	}

	// Verify the file exists.
	if _, err := os.Stat(pathA); err != nil {
		t.Fatalf("file A should exist: %v", err)
	}

	// Add B to trigger eviction of A.
	pathB, _ := c.Reserve("asset://B")
	if err := os.WriteFile(pathB, make([]byte, 60), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}
	entryB := &CacheEntry{
		AssetURI:     "asset://B",
		LocalPath:    pathB,
		State:        AssetCached,
		ByteSize:     60,
		LastAccessed: time.Now(),
	}
	if err := c.Put(entryB); err != nil {
		t.Fatalf("Put B: %v", err)
	}

	// File A should have been deleted from disk.
	if _, err := os.Stat(pathA); !os.IsNotExist(err) {
		t.Errorf("file A should be deleted from disk, but stat returned: %v", err)
	}
}

func TestCacheStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	// Add a cached item.
	pathA, _ := c.Reserve("asset://A")
	_ = os.WriteFile(pathA, make([]byte, 100), 0o644)
	_ = c.Put(&CacheEntry{
		AssetURI:     "asset://A",
		LocalPath:    pathA,
		State:        AssetCached,
		ByteSize:     100,
		LastAccessed: time.Now(),
	})

	// Add a fetching item (no byte size yet).
	pathB, _ := c.Reserve("asset://B")
	_ = c.Put(&CacheEntry{
		AssetURI:     "asset://B",
		LocalPath:    pathB,
		State:        AssetFetching,
		ByteSize:     0,
		LastAccessed: time.Now(),
	})

	// Add an error item.
	pathC, _ := c.Reserve("asset://C")
	_ = c.Put(&CacheEntry{
		AssetURI:     "asset://C",
		LocalPath:    pathC,
		State:        AssetError,
		ByteSize:     0,
		ErrorMsg:     "fetch failed",
		LastAccessed: time.Now(),
	})

	status := c.Status()
	if status.ItemCount != 3 {
		t.Errorf("ItemCount = %d, want 3", status.ItemCount)
	}
	if status.UsedBytes != 100 {
		t.Errorf("UsedBytes = %d, want 100", status.UsedBytes)
	}
	if status.FetchingCount != 1 {
		t.Errorf("FetchingCount = %d, want 1", status.FetchingCount)
	}
	if status.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", status.ErrorCount)
	}
	if status.MaxBytes != 1<<20 {
		t.Errorf("MaxBytes = %d, want %d", status.MaxBytes, 1<<20)
	}
	if status.CacheDir != dir {
		t.Errorf("CacheDir = %q, want %q", status.CacheDir, dir)
	}
}

func TestCacheEvict(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	localPath, _ := c.Reserve("asset://A")
	_ = os.WriteFile(localPath, make([]byte, 100), 0o644)

	_ = c.Put(&CacheEntry{
		AssetURI:     "asset://A",
		LocalPath:    localPath,
		State:        AssetCached,
		ByteSize:     100,
		LastAccessed: time.Now(),
	})

	// Manual evict.
	if err := c.Evict("asset://A"); err != nil {
		t.Fatalf("Evict: %v", err)
	}

	// Verify gone from cache.
	_, err = c.Get("asset://A")
	if err != ErrAssetNotFound {
		t.Errorf("Get after Evict = %v, want ErrAssetNotFound", err)
	}

	// Verify file deleted.
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Errorf("file should be deleted, stat returned: %v", err)
	}

	// Evicting again should return not found.
	if err := c.Evict("asset://A"); err != ErrAssetNotFound {
		t.Errorf("second Evict = %v, want ErrAssetNotFound", err)
	}
}

func TestCachePathTraversal(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	// Try to put an entry with a path outside the cache dir.
	outsidePath := filepath.Join(t.TempDir(), "evil.ts")
	_ = os.WriteFile(outsidePath, make([]byte, 100), 0o644)

	err = c.Put(&CacheEntry{
		AssetURI:     "asset://evil",
		LocalPath:    outsidePath,
		State:        AssetCached,
		ByteSize:     100,
		LastAccessed: time.Now(),
	})
	if err != ErrPathTraversal {
		t.Errorf("Put with outside path = %v, want ErrPathTraversal", err)
	}

	// Try with a ".." path component.
	traversalPath := filepath.Join(dir, "assets", "..", "..", "etc", "passwd")
	err = c.Put(&CacheEntry{
		AssetURI:     "asset://traversal",
		LocalPath:    traversalPath,
		State:        AssetCached,
		ByteSize:     100,
		LastAccessed: time.Now(),
	})
	if err != ErrPathTraversal {
		t.Errorf("Put with traversal path = %v, want ErrPathTraversal", err)
	}

	// Entry should not be stored.
	_, err = c.Get("asset://evil")
	if err != ErrAssetNotFound {
		t.Errorf("Get evil = %v, want ErrAssetNotFound", err)
	}
}

func TestCachePersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	// Create cache and add an item.
	c1, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache (1): %v", err)
	}

	localPath, _ := c1.Reserve("s3://bucket/persist.ts")
	_ = os.WriteFile(localPath, make([]byte, 200), 0o644)

	_ = c1.Put(&CacheEntry{
		AssetURI:     "s3://bucket/persist.ts",
		LocalPath:    localPath,
		State:        AssetCached,
		ByteSize:     200,
		Checksum:     "deadbeef",
		LastAccessed: time.Now(),
		FetchedAt:    time.Now(),
	})
	c1.Close()

	// Reopen the cache.
	c2, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache (2): %v", err)
	}
	defer c2.Close()

	got, err := c2.Get("s3://bucket/persist.ts")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.ByteSize != 200 {
		t.Errorf("ByteSize = %d, want 200", got.ByteSize)
	}
	if got.Checksum != "deadbeef" {
		t.Errorf("Checksum = %q, want %q", got.Checksum, "deadbeef")
	}
	if got.State != AssetCached {
		t.Errorf("State = %v, want %v", got.State, AssetCached)
	}
}

func TestCacheReserve(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	path, err := c.Reserve("s3://bucket/video.mp4")
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// Path should be under cache dir.
	assetsDir := filepath.Join(dir, "assets")
	if !isUnderDir(path, assetsDir) {
		t.Errorf("reserved path %q not under assets dir %q", path, assetsDir)
	}

	// Should preserve extension.
	if ext := filepath.Ext(path); ext != ".mp4" {
		t.Errorf("extension = %q, want .mp4", ext)
	}

	// Assets dir should have been created.
	if _, err := os.Stat(assetsDir); err != nil {
		t.Errorf("assets dir not created: %v", err)
	}
}

func TestCacheReserveDeterministic(t *testing.T) {
	dir := t.TempDir()
	cfg := CacheConfig{Dir: dir, MaxBytes: 1 << 20}

	c, err := NewCache(cfg)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	defer c.Close()

	path1, _ := c.Reserve("s3://bucket/video.mp4")
	path2, _ := c.Reserve("s3://bucket/video.mp4")

	if path1 != path2 {
		t.Errorf("Reserve not deterministic: %q != %q", path1, path2)
	}

	// Different URI → different path.
	path3, _ := c.Reserve("s3://bucket/other.mp4")
	if path1 == path3 {
		t.Errorf("different URIs produced same path: %q", path1)
	}
}

// isUnderDir checks if path is under dir using the same logic the cache uses.
func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && rel[:2] != ".."
}
