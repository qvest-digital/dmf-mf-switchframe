// Package clip provides media clip storage and playback for the Switchframe switcher.
// Clips are MPEG-TS or MP4 files that can be loaded into player slots and played
// to program output. The Store manages clip metadata persistence and file lifecycle.
package clip

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store manages CRUD operations and file persistence for media clips.
// Clip metadata is stored as JSON in clips.json within the store directory.
// Media files are stored alongside the metadata file.
type Store struct {
	mu       sync.RWMutex
	clips    map[string]*Clip
	dir      string
	filePath string
	maxBytes int64
}

// NewStore creates a Store backed by the given directory with a storage limit in bytes.
// If clips.json exists in the directory, clips are loaded from it.
// If the directory does not exist, it is created.
func NewStore(dir string, maxBytes int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create clip dir: %w", err)
	}

	s := &Store{
		clips:    make(map[string]*Clip),
		dir:      dir,
		filePath: filepath.Join(dir, "clips.json"),
		maxBytes: maxBytes,
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read clips file: %w", err)
	}

	var clips []*Clip
	if err := json.Unmarshal(data, &clips); err != nil {
		return nil, fmt.Errorf("parse clips file: %w", err)
	}
	for _, c := range clips {
		s.clips[c.ID] = c
	}

	return s, nil
}

// Add inserts a new clip into the store. The clip's ID and CreatedAt fields
// are assigned automatically. Non-ephemeral clips are checked against the
// storage limit; ephemeral clips bypass this check.
func (s *Store) Add(c *Clip) error {
	id, err := generateID()
	if err != nil {
		return fmt.Errorf("generate clip ID: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check storage limit for non-ephemeral clips.
	if !c.Ephemeral {
		used := s.totalBytesLocked()
		if used+c.ByteSize > s.maxBytes {
			return ErrStorageFull
		}
	}

	c.ID = id
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	s.clips[c.ID] = c

	if err := s.save(); err != nil {
		delete(s.clips, c.ID)
		return fmt.Errorf("save clips: %w", err)
	}
	return nil
}

// Get returns a clip by ID. Returns ErrNotFound if the clip does not exist.
func (s *Store) Get(id string) (*Clip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clips[id]
	if !ok {
		return nil, ErrNotFound
	}
	// Return a copy to prevent callers from modifying store state.
	cp := *c
	return &cp, nil
}

// List returns all clips sorted by CreatedAt descending (newest first).
func (s *Store) List() []*Clip {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Clip, 0, len(s.clips))
	for _, c := range s.clips {
		cp := *c
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

// Update applies a mutator function to the clip with the given ID, then persists.
// Returns ErrNotFound if the clip does not exist.
func (s *Store) Update(id string, fn func(*Clip)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clips[id]
	if !ok {
		return ErrNotFound
	}

	backup := *c
	fn(c)

	if err := s.save(); err != nil {
		*c = backup // rollback
		return fmt.Errorf("save clips: %w", err)
	}
	return nil
}

// Delete removes a clip by ID, deletes the associated media file, and persists.
// Returns ErrNotFound if the clip does not exist.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clips[id]
	if !ok {
		return ErrNotFound
	}

	filename := c.Filename
	delete(s.clips, id)

	if err := s.save(); err != nil {
		return fmt.Errorf("save clips: %w", err)
	}

	// Best-effort file deletion; metadata is already removed.
	if filename != "" {
		_ = os.Remove(filepath.Join(s.dir, filename))
	}
	return nil
}

// Pin marks an ephemeral clip as permanent by setting Ephemeral to false.
// Returns ErrNotFound if the clip does not exist, or ErrStorageFull if
// pinning would exceed the storage limit.
func (s *Store) Pin(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clips[id]
	if !ok {
		return ErrNotFound
	}
	if !c.Ephemeral {
		return nil // already pinned
	}

	used := s.totalBytesLocked()
	if used+c.ByteSize > s.maxBytes {
		return ErrStorageFull
	}

	c.Ephemeral = false
	if err := s.save(); err != nil {
		c.Ephemeral = true // rollback
		return fmt.Errorf("save clips: %w", err)
	}
	return nil
}

// CleanEphemeral removes ephemeral clips older than maxAge.
// Clips whose IDs appear in loadedIDs are skipped (they are in active use).
// Returns the number of clips removed.
func (s *Store) CleanEphemeral(maxAge time.Duration, loadedIDs map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var toDelete []string

	for id, c := range s.clips {
		if !c.Ephemeral {
			continue
		}
		if loadedIDs != nil && loadedIDs[id] {
			continue
		}
		if c.CreatedAt.Before(cutoff) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		c := s.clips[id]
		if c.Filename != "" {
			_ = os.Remove(filepath.Join(s.dir, c.Filename))
		}
		delete(s.clips, id)
	}

	if len(toDelete) > 0 {
		_ = s.save()
	}

	return len(toDelete)
}

// Dir returns the store directory path. Used by the Manager to locate
// clip media files for demuxing.
func (s *Store) Dir() string {
	return s.dir
}

// TotalBytes returns the sum of ByteSize for all non-ephemeral clips.
func (s *Store) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytesLocked()
}

// totalBytesLocked returns the sum of ByteSize for non-ephemeral clips.
// Must be called with s.mu held.
func (s *Store) totalBytesLocked() int64 {
	var total int64
	for _, c := range s.clips {
		if !c.Ephemeral {
			total += c.ByteSize
		}
	}
	return total
}

// save writes the current clips to disk atomically (temp file + fsync + rename).
// Must be called with s.mu held.
func (s *Store) save() error {
	clips := make([]*Clip, 0, len(s.clips))
	for _, c := range s.clips {
		clips = append(clips, c)
	}
	// Sort for deterministic output.
	sort.Slice(clips, func(i, j int) bool {
		return clips[i].CreatedAt.Before(clips[j].CreatedAt)
	})

	data, err := json.MarshalIndent(clips, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal clips: %w", err)
	}

	tmpFile, err := os.CreateTemp(s.dir, "clips-*.tmp")
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

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// generateID creates a random identifier (16 bytes hex = 32 chars).
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
