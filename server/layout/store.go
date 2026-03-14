package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// Store manages CRUD operations for custom layout presets.
type Store struct {
	mu       sync.RWMutex
	presets  map[string]*Layout
	filePath string
}

// NewStore creates a new layout preset store, loading from file if it exists.
func NewStore(filePath string) *Store {
	s := &Store{
		presets:  make(map[string]*Layout),
		filePath: filePath,
	}
	s.load()
	return s
}

// Save stores a layout preset. Overwrites if name already exists.
func (s *Store) Save(l *Layout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presets[l.Name] = l
	return s.persist()
}

// Get returns a layout preset by name.
func (s *Store) Get(name string) (*Layout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.presets[name]
	if !ok {
		return nil, fmt.Errorf("layout preset %q not found", name)
	}
	cp := *l
	cp.Slots = make([]Slot, len(l.Slots))
	copy(cp.Slots, l.Slots)
	return &cp, nil
}

// Delete removes a layout preset by name.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.presets[name]; !ok {
		return fmt.Errorf("layout preset %q not found", name)
	}
	delete(s.presets, name)
	return s.persist()
}

// List returns the names of all saved presets.
func (s *Store) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.presets))
	for name := range s.presets {
		names = append(names, name)
	}
	return names
}

func (s *Store) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("failed to load layout presets", "error", err, "path", s.filePath)
		}
		return
	}
	var presets map[string]*Layout
	if err := json.Unmarshal(data, &presets); err != nil {
		slog.Warn("failed to parse layout presets", "error", err, "path", s.filePath)
		return
	}
	s.presets = presets
}

func (s *Store) persist() error {
	data, err := json.MarshalIndent(s.presets, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}
