package playout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cache is an LRU local disk cache for encoded media assets.
// Files are stored on local SSD as the "warm" tier between cold storage
// and the hot streaming decoder.
type Cache struct {
	mu           sync.Mutex
	entries      map[string]*CacheEntry
	accessOrder  []string // oldest-accessed first
	usedBytes    int64    // running total of ByteSize across all entries
	config       CacheConfig
	metadataPath string
}

// NewCache creates a new asset cache backed by the given directory.
// If cache_meta.json exists in the directory, entries are loaded from it.
// If the directory does not exist, it is created.
func NewCache(config CacheConfig) (*Cache, error) {
	if err := os.MkdirAll(config.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	c := &Cache{
		entries:      make(map[string]*CacheEntry),
		config:       config,
		metadataPath: filepath.Join(config.Dir, "cache_meta.json"),
	}

	// Load existing metadata if present.
	data, err := os.ReadFile(c.metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read cache metadata: %w", err)
	}

	var entries []*CacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse cache metadata: %w", err)
	}

	for _, e := range entries {
		c.entries[e.AssetURI] = e
	}
	c.rebuildAccessOrder()

	return c, nil
}

// Put adds or updates a cache entry. The entry's LocalPath must be under
// the cache directory (path traversal prevention). After insertion, LRU
// eviction runs if total cached bytes exceed MaxBytes.
func (c *Cache) Put(entry *CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Path traversal prevention: LocalPath must be under cache dir.
	if err := c.validatePath(entry.LocalPath); err != nil {
		return err
	}

	// Store a copy to prevent caller mutation.
	cp := *entry

	// Update running total: subtract old size if replacing an existing entry.
	if old, ok := c.entries[cp.AssetURI]; ok {
		c.usedBytes -= old.ByteSize
	}
	c.entries[cp.AssetURI] = &cp
	c.usedBytes += cp.ByteSize

	// Update access order: remove old position, append to end.
	c.removeFromAccessOrder(cp.AssetURI)
	c.accessOrder = append(c.accessOrder, cp.AssetURI)

	// Run LRU eviction if over capacity.
	c.evictLRU()

	return c.persist()
}

// Get returns a copy of the cache entry for the given URI.
// Returns ErrAssetNotFound if the URI is not in the cache.
func (c *Cache) Get(uri string) (*CacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[uri]
	if !ok {
		return nil, ErrAssetNotFound
	}

	// Touch: update last accessed time and move to end of access order.
	e.LastAccessed = time.Now()
	c.removeFromAccessOrder(uri)
	c.accessOrder = append(c.accessOrder, uri)

	// Return a copy.
	cp := *e
	return &cp, nil
}

// Peek returns a copy of the cache entry without updating LRU order or
// LastAccessed time. Used for read-without-touch operations like prefetch scans.
// Returns ErrAssetNotFound if the URI is not in the cache.
func (c *Cache) Peek(uri string) (*CacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[uri]
	if !ok {
		return nil, ErrAssetNotFound
	}

	// Return a copy without touching LRU state.
	cp := *e
	return &cp, nil
}

// Evict removes an entry from the cache and deletes its file on disk.
// Returns ErrAssetNotFound if the URI is not in the cache.
func (c *Cache) Evict(uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[uri]
	if !ok {
		return ErrAssetNotFound
	}

	// Delete the file on disk (best effort).
	if e.LocalPath != "" {
		_ = os.Remove(e.LocalPath)
	}

	c.usedBytes -= e.ByteSize
	delete(c.entries, uri)
	c.removeFromAccessOrder(uri)

	return c.persist()
}

// Status returns aggregate cache statistics.
func (c *Cache) Status() CacheStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	var pending, fetching, errCount int

	for _, e := range c.entries {
		switch e.State {
		case AssetPending:
			pending++
		case AssetFetching, AssetTranscoding:
			fetching++
		case AssetError:
			errCount++
		}
	}

	return CacheStatus{
		CacheDir:      c.config.Dir,
		MaxBytes:      c.config.MaxBytes,
		UsedBytes:     c.usedBytes,
		ItemCount:     len(c.entries),
		PendingCount:  pending,
		FetchingCount: fetching,
		ErrorCount:    errCount,
	}
}

// Reserve generates a safe local file path for a new asset using SHA256
// of the URI for the filename. The original file extension is preserved.
// The path is under <cacheDir>/assets/.
func (c *Cache) Reserve(uri string) (string, error) {
	assetsDir := filepath.Join(c.config.Dir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return "", fmt.Errorf("create assets dir: %w", err)
	}

	// SHA256 of the URI for the filename.
	h := sha256.Sum256([]byte(uri))
	name := hex.EncodeToString(h[:])

	// Preserve the original extension.
	ext := filepath.Ext(uri)
	if ext != "" {
		name += ext
	}

	return filepath.Join(assetsDir, name), nil
}

// Close persists metadata to disk.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.persist()
}

// validatePath checks that the given path is under the cache directory.
// Returns ErrPathTraversal if the path escapes the cache dir.
func (c *Cache) validatePath(path string) error {
	// Clean both paths for consistent comparison.
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(c.config.Dir)

	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return ErrPathTraversal
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ErrPathTraversal
	}
	return nil
}

// evictLRU removes the oldest-accessed cached entries until total bytes
// are under MaxBytes. Entries in Fetching or Transcoding state are skipped.
// Must be called with c.mu held.
func (c *Cache) evictLRU() {
	for c.usedBytes > c.config.MaxBytes && len(c.accessOrder) > 0 {
		evicted := false
		for i, uri := range c.accessOrder {
			e, ok := c.entries[uri]
			if !ok {
				// Stale entry in access order — remove it.
				c.accessOrder = append(c.accessOrder[:i], c.accessOrder[i+1:]...)
				evicted = true
				break
			}
			// Skip entries that are still being fetched or transcoded.
			if e.State == AssetFetching || e.State == AssetTranscoding {
				continue
			}
			// Evict this entry.
			if e.LocalPath != "" {
				_ = os.Remove(e.LocalPath)
			}
			c.usedBytes -= e.ByteSize
			delete(c.entries, uri)
			c.accessOrder = append(c.accessOrder[:i], c.accessOrder[i+1:]...)
			evicted = true
			break
		}
		if !evicted {
			// No evictable entries remain.
			break
		}
	}
}

// removeFromAccessOrder removes the given URI from the access order slice.
// Must be called with c.mu held.
func (c *Cache) removeFromAccessOrder(uri string) {
	for i, u := range c.accessOrder {
		if u == uri {
			c.accessOrder = append(c.accessOrder[:i], c.accessOrder[i+1:]...)
			return
		}
	}
}

// rebuildAccessOrder rebuilds the accessOrder slice from loaded entries,
// sorted by LastAccessed (oldest first). Also recomputes usedBytes from
// loaded entries.
func (c *Cache) rebuildAccessOrder() {
	c.accessOrder = make([]string, 0, len(c.entries))
	c.usedBytes = 0
	for uri, e := range c.entries {
		c.accessOrder = append(c.accessOrder, uri)
		c.usedBytes += e.ByteSize
	}
	sort.Slice(c.accessOrder, func(i, j int) bool {
		ei := c.entries[c.accessOrder[i]]
		ej := c.entries[c.accessOrder[j]]
		return ei.LastAccessed.Before(ej.LastAccessed)
	})
}

// persist writes cache metadata to disk atomically (temp file + fsync + rename).
// Must be called with c.mu held.
func (c *Cache) persist() error {
	entries := make([]*CacheEntry, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, e)
	}
	// Sort for deterministic output.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastAccessed.Before(entries[j].LastAccessed)
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache metadata: %w", err)
	}

	tmpFile, err := os.CreateTemp(c.config.Dir, "cache_meta-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, c.metadataPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
