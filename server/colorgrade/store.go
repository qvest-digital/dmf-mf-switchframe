package colorgrade

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// ErrBuiltinLook is returned when attempting to delete a built-in look.
	ErrBuiltinLook = errors.New("cannot delete built-in look")

	// ErrLookNotFound is returned when a look does not exist.
	ErrLookNotFound = errors.New("look not found")

	validNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
)

// LookInfo describes an available color look.
type LookInfo struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	Path    string `json:"-"`
}

// Store manages built-in and uploaded .cube LUT files.
type Store struct {
	builtinDir string
	uploadDir  string
}

// NewStore creates a store. builtinDir contains shipped .cube files (read-only).
// uploadDir is where user-uploaded .cube files are saved.
func NewStore(builtinDir, uploadDir string) (*Store, error) {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("colorgrade store: create upload dir: %w", err)
	}
	return &Store{
		builtinDir: builtinDir,
		uploadDir:  uploadDir,
	}, nil
}

// List returns all available looks (built-in + uploaded), sorted by name.
func (s *Store) List() []LookInfo {
	var looks []LookInfo

	// Scan built-in directory.
	if entries, err := os.ReadDir(s.builtinDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cube") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".cube")
			looks = append(looks, LookInfo{
				Name:    name,
				Builtin: true,
				Path:    filepath.Join(s.builtinDir, e.Name()),
			})
		}
	}

	// Scan upload directory.
	if entries, err := os.ReadDir(s.uploadDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cube") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".cube")
			looks = append(looks, LookInfo{
				Name:    name,
				Builtin: false,
				Path:    filepath.Join(s.uploadDir, e.Name()),
			})
		}
	}

	sort.Slice(looks, func(i, j int) bool {
		return looks[i].Name < looks[j].Name
	})
	return looks
}

// Load parses and returns the LUT3D for the named look.
// Searches built-in directory first, then uploads.
func (s *Store) Load(name string) (*LUT3D, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}

	// Try built-in first.
	path := filepath.Join(s.builtinDir, name+".cube")
	if _, err := os.Stat(path); err == nil {
		return s.loadFile(path)
	}

	// Try uploaded.
	path = filepath.Join(s.uploadDir, name+".cube")
	if _, err := os.Stat(path); err == nil {
		return s.loadFile(path)
	}

	return nil, fmt.Errorf("colorgrade store: look %q: %w", name, ErrLookNotFound)
}

// Upload validates and saves a .cube file under the given name.
func (s *Store) Upload(name string, data io.Reader) error {
	if err := validateName(name); err != nil {
		return err
	}

	// Parse to validate format before saving.
	lut, err := ParseCube(data)
	if err != nil {
		return fmt.Errorf("colorgrade store: invalid .cube file: %w", err)
	}

	path := filepath.Join(s.uploadDir, name+".cube")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("colorgrade store: create file: %w", err)
	}
	defer f.Close()

	if err := WriteCube(f, lut); err != nil {
		os.Remove(path)
		return fmt.Errorf("colorgrade store: write file: %w", err)
	}

	return nil
}

// Delete removes an uploaded look. Returns an error if the look is built-in.
func (s *Store) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}

	// Check if it's a built-in look.
	builtinPath := filepath.Join(s.builtinDir, name+".cube")
	if _, err := os.Stat(builtinPath); err == nil {
		return fmt.Errorf("colorgrade store: %q: %w", name, ErrBuiltinLook)
	}

	uploadPath := filepath.Join(s.uploadDir, name+".cube")
	if _, err := os.Stat(uploadPath); err != nil {
		return fmt.Errorf("colorgrade store: %q: %w", name, ErrLookNotFound)
	}

	if err := os.Remove(uploadPath); err != nil {
		return fmt.Errorf("colorgrade store: delete: %w", err)
	}
	return nil
}

func (s *Store) loadFile(path string) (*LUT3D, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("colorgrade store: open: %w", err)
	}
	defer f.Close()

	lut, err := ParseCube(f)
	if err != nil {
		return nil, fmt.Errorf("colorgrade store: parse %s: %w", filepath.Base(path), err)
	}
	return lut, nil
}

func validateName(name string) error {
	if !validNameRe.MatchString(name) {
		return fmt.Errorf("colorgrade store: invalid name %q (must be alphanumeric with hyphens/underscores)", name)
	}
	if len(name) > 64 {
		return fmt.Errorf("colorgrade store: name too long (max 64 chars)")
	}
	return nil
}
