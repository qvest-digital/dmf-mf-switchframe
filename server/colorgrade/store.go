package colorgrade

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
	Path        string `json:"-"` // internal, not serialized
}

// Store manages built-in (embedded) and uploaded .cube LUT files.
type Store struct {
	uploadDir string
	builtins  map[string][]byte      // name → raw .cube content
	metadata  map[string]*LookParams // name → params (for Group/Description)
}

// NewStore creates a store with embedded built-in looks and a filesystem
// directory for user uploads.
func NewStore(uploadDir string) (*Store, error) {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("colorgrade store: create upload dir: %w", err)
	}

	builtins := make(map[string][]byte)
	entries, err := fs.ReadDir(builtinLooksFS, "looks")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cube") {
				continue
			}
			data, err := fs.ReadFile(builtinLooksFS, "looks/"+e.Name())
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".cube")
			builtins[name] = data
		}
	}

	// Build metadata lookup from BuiltinLooks() for Group/Description.
	allLooks := BuiltinLooks()
	meta := make(map[string]*LookParams, len(allLooks))
	for i := range allLooks {
		meta[allLooks[i].Name] = &allLooks[i]
	}

	return &Store{
		uploadDir: uploadDir,
		builtins:  builtins,
		metadata:  meta,
	}, nil
}

// List returns all available looks (built-in + uploaded), sorted by name.
func (s *Store) List() []LookInfo {
	var looks []LookInfo

	// Built-in looks from embedded FS.
	for name := range s.builtins {
		info := LookInfo{
			Name:    name,
			Builtin: true,
		}
		if p, ok := s.metadata[name]; ok {
			info.Group = p.Group
			info.Description = p.Description
		}
		looks = append(looks, info)
	}

	// Scan upload directory.
	if entries, err := os.ReadDir(s.uploadDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cube") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".cube")
			looks = append(looks, LookInfo{
				Name:        name,
				Group:       "Custom",
				Description: "Custom look",
				Builtin:     false,
				Path:        filepath.Join(s.uploadDir, e.Name()),
			})
		}
	}

	sort.Slice(looks, func(i, j int) bool {
		return looks[i].Name < looks[j].Name
	})
	return looks
}

// Load parses and returns the LUT3D for the named look.
// Searches built-in looks first, then uploads.
func (s *Store) Load(name string) (*LUT3D, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}

	// Try built-in first.
	if data, ok := s.builtins[name]; ok {
		return ParseCube(bytes.NewReader(data))
	}

	// Try uploaded.
	path := filepath.Join(s.uploadDir, name+".cube")
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

	if err := WriteCube(f, lut); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("colorgrade store: write file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("colorgrade store: close file: %w", err)
	}
	return nil
}

// Delete removes an uploaded look. Returns an error if the look is built-in.
func (s *Store) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}

	// Check if it's a built-in look.
	if _, ok := s.builtins[name]; ok {
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
	defer func() { _ = f.Close() }()

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
