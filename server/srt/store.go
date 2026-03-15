package srt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type storeData struct {
	Sources map[string]SourceConfig `json:"sources"`
}

// Store manages CRUD operations and file persistence for SRT source configs.
// It uses file-based JSON with sync.RWMutex and atomic temp-file + rename writes.
type Store struct {
	mu       sync.RWMutex
	filePath string
	data     storeData
}

// NewStore creates a Store that persists to the given file path.
// If the file exists, configs are loaded from it. If it does not exist,
// the store starts empty and the file is created on first mutation.
func NewStore(filePath string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		data:     storeData{Sources: make(map[string]SourceConfig)},
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read srt sources file: %w", err)
	}

	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse srt sources file: %w", err)
	}
	if s.data.Sources == nil {
		s.data.Sources = make(map[string]SourceConfig)
	}

	return s, nil
}

// Get returns the SourceConfig for the given key, or false if not found.
func (s *Store) Get(key string) (SourceConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.data.Sources[key]
	return cfg, ok
}

// List returns all stored SourceConfigs.
func (s *Store) List() []SourceConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SourceConfig, 0, len(s.data.Sources))
	for _, cfg := range s.data.Sources {
		out = append(out, cfg)
	}
	return out
}

// Save validates and persists a SourceConfig. If a config with the same key
// exists, it is replaced.
func (s *Store) Save(cfg SourceConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Sources[cfg.Key] = cfg
	if err := s.flush(); err != nil {
		// Roll back in-memory state on disk write failure.
		delete(s.data.Sources, cfg.Key)
		return err
	}
	return nil
}

// Delete removes a SourceConfig by key and persists the change.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, had := s.data.Sources[key]
	delete(s.data.Sources, key)
	if err := s.flush(); err != nil {
		// Roll back in-memory state on disk write failure.
		if had {
			s.data.Sources[key] = prev
		}
		return err
	}
	return nil
}

// flush writes the current in-memory data to disk atomically (temp file + rename).
// Must be called with s.mu held.
func (s *Store) flush() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal srt sources: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "srt-sources-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(raw); err != nil {
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
