package operator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store manages operator registration and file persistence.
// Follows the macro/store.go pattern: RWMutex + atomic temp-file + rename.
type Store struct {
	mu        sync.RWMutex
	operators []Operator
	tokenIdx  map[string]int // token → index in operators slice
	filePath  string
}

// NewStore creates a Store that persists to the given file path.
// If the file exists, operators are loaded from it. If it does not exist,
// the store starts empty and the file is created on first mutation.
func NewStore(filePath string) (*Store, error) {
	s := &Store{
		filePath:  filePath,
		operators: []Operator{},
		tokenIdx:  make(map[string]int),
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read operators file: %w", err)
	}

	if err := json.Unmarshal(data, &s.operators); err != nil {
		return nil, fmt.Errorf("parse operators file: %w", err)
	}
	if s.operators == nil {
		s.operators = []Operator{}
	}

	s.rebuildTokenIndex()
	return s, nil
}

// rebuildTokenIndex reconstructs the token→index map from the operators slice.
// Must be called with s.mu held.
func (s *Store) rebuildTokenIndex() {
	s.tokenIdx = make(map[string]int, len(s.operators))
	for i, op := range s.operators {
		s.tokenIdx[op.Token] = i
	}
}

// Register creates a new operator with a per-operator bearer token.
func (s *Store) Register(name string, role Role) (Operator, error) {
	if name == "" {
		return Operator{}, ErrEmptyName
	}
	if !ValidRoles[role] {
		return Operator{}, ErrInvalidRole
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate name.
	for _, op := range s.operators {
		if op.Name == name {
			return Operator{}, ErrDuplicateName
		}
	}

	id, err := generateID()
	if err != nil {
		return Operator{}, fmt.Errorf("generate ID: %w", err)
	}
	token, err := generateToken()
	if err != nil {
		return Operator{}, fmt.Errorf("generate token: %w", err)
	}

	op := Operator{
		ID:    id,
		Name:  name,
		Role:  role,
		Token: token,
	}
	s.operators = append(s.operators, op)
	s.tokenIdx[op.Token] = len(s.operators) - 1

	if err := s.save(); err != nil {
		// Roll back: remove the appended operator and its token index entry.
		s.operators = s.operators[:len(s.operators)-1]
		delete(s.tokenIdx, op.Token)
		return Operator{}, fmt.Errorf("save operators: %w", err)
	}
	return op, nil
}

// List returns all registered operators.
func (s *Store) List() []Operator {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Operator, len(s.operators))
	copy(result, s.operators)
	return result
}

// Get returns an operator by ID.
func (s *Store) Get(id string) (Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, op := range s.operators {
		if op.ID == id {
			return op, nil
		}
	}
	return Operator{}, ErrNotFound
}

// GetByToken returns an operator by their bearer token.
// Uses a token index map for O(1) lookup.
func (s *Store) GetByToken(token string) (Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.tokenIdx[token]
	if ok && idx < len(s.operators) {
		return s.operators[idx], nil
	}
	return Operator{}, ErrNotFound
}

// Delete removes an operator by ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, op := range s.operators {
		if op.ID == id {
			// Save a copy for rollback before mutating.
			removed := s.operators[i]
			s.operators = append(s.operators[:i], s.operators[i+1:]...)
			s.rebuildTokenIndex()
			if err := s.save(); err != nil {
				// Roll back: re-insert at original position and rebuild index.
				rear := make([]Operator, len(s.operators[i:]))
				copy(rear, s.operators[i:])
				s.operators = append(s.operators[:i], removed)
				s.operators = append(s.operators, rear...)
				s.rebuildTokenIndex()
				return fmt.Errorf("save operators: %w", err)
			}
			return nil
		}
	}
	return ErrNotFound
}

// Count returns the number of registered operators.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.operators)
}

// save writes operators to disk atomically (temp file + rename).
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.operators, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operators: %w", err)
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, "operators-*.tmp")
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

// generateID creates a random UUID-like identifier (16 bytes hex = 32 chars).
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateToken creates a random bearer token (32 bytes hex = 64 chars).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
