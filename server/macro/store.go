package macro

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrNotFound is returned when a macro with the given name does not exist.
	ErrNotFound = errors.New("macro: not found")

	// ErrEmptyName is returned when a macro name is empty.
	ErrEmptyName = errors.New("macro: name must not be empty")

	// ErrNoSteps is returned when a macro has no steps.
	ErrNoSteps = errors.New("macro: must have at least one step")
)

// Store manages CRUD operations and file persistence for macros.
// It mirrors the preset.Store pattern: file-based JSON with
// sync.RWMutex and atomic temp-file + rename writes.
type Store struct {
	mu       sync.RWMutex
	macros   []Macro
	filePath string
}

// NewStore creates a Store that persists to the given file path.
// If the file exists, macros are loaded from it. If it does not exist,
// the store starts empty and the file is created on first mutation.
func NewStore(filePath string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		macros:   []Macro{},
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read macros file: %w", err)
	}

	if err := json.Unmarshal(data, &s.macros); err != nil {
		return nil, fmt.Errorf("parse macros file: %w", err)
	}
	if s.macros == nil {
		s.macros = []Macro{}
	}

	return s, nil
}

// List returns all macros.
func (s *Store) List() []Macro {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Macro, len(s.macros))
	copy(result, s.macros)
	return result
}

// Get returns a macro by name.
func (s *Store) Get(name string) (Macro, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.macros {
		if m.Name == name {
			return m, nil
		}
	}
	return Macro{}, ErrNotFound
}

// Save creates or updates a macro. If a macro with the same name exists,
// it is replaced.
func (s *Store) Save(m Macro) error {
	if m.Name == "" {
		return ErrEmptyName
	}
	if len(m.Steps) == 0 {
		return ErrNoSteps
	}

	result := ValidateSteps(m.Steps)
	if result.HasErrors() {
		return &result.Errors[0]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Replace existing macro with same name, or append.
	var previous Macro
	replaceIdx := -1
	for i := range s.macros {
		if s.macros[i].Name == m.Name {
			previous = s.macros[i]
			replaceIdx = i
			s.macros[i] = m
			break
		}
	}
	if replaceIdx == -1 {
		s.macros = append(s.macros, m)
	}

	if err := s.save(); err != nil {
		// Roll back: restore previous state.
		if replaceIdx >= 0 {
			s.macros[replaceIdx] = previous
		} else {
			s.macros = s.macros[:len(s.macros)-1]
		}
		return fmt.Errorf("save macros: %w", err)
	}
	return nil
}

// Delete removes a macro by name.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, m := range s.macros {
		if m.Name == name {
			removed := s.macros[i]
			s.macros = append(s.macros[:i], s.macros[i+1:]...)
			if err := s.save(); err != nil {
				// Roll back: re-insert at original position.
				rear := make([]Macro, len(s.macros[i:]))
				copy(rear, s.macros[i:])
				s.macros = append(s.macros[:i], removed)
				s.macros = append(s.macros, rear...)
				return fmt.Errorf("save macros: %w", err)
			}
			return nil
		}
	}
	return ErrNotFound
}

// save writes the current macros to disk atomically (temp file + rename).
// Must be called with s.mu held (either read or write lock).
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.macros, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal macros: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "macros-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
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
