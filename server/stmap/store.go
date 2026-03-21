package stmap

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	staticExt   = ".stmap"
	animatedExt = ".stmap-anim.json"
)

// Store persists ST maps to a directory on disk.
type Store struct {
	dir string
}

// NewStore creates a store backed by the given directory. Creates the dir if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("stmap store: create directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

// SaveStatic writes a static ST map as raw float32 binary.
// File format: [uint32 BE width][uint32 BE height][float32[] S plane LE][float32[] T plane LE]
// Filename: {dir}/{name}.stmap
func (s *Store) SaveStatic(m *STMap) error {
	if err := ValidateName(m.Name); err != nil {
		return err
	}

	n := m.Width * m.Height
	// Header: 4 bytes width + 4 bytes height.
	// Data: n*4 bytes S plane + n*4 bytes T plane.
	size := 8 + n*4*2
	buf := make([]byte, size)

	// Write header (big-endian).
	binary.BigEndian.PutUint32(buf[0:4], uint32(m.Width))
	binary.BigEndian.PutUint32(buf[4:8], uint32(m.Height))

	// Write S plane (little-endian float32).
	off := 8
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(m.S[i]))
		off += 4
	}

	// Write T plane (little-endian float32).
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(m.T[i]))
		off += 4
	}

	path := filepath.Join(s.dir, m.Name+staticExt)
	return os.WriteFile(path, buf, 0o644)
}

// LoadStatic reads a static ST map from disk.
func (s *Store) LoadStatic(name string) (*STMap, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	path := filepath.Join(s.dir, name+staticExt)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("stmap store: read %s: %w", name, err)
	}

	if len(data) < 8 {
		return nil, fmt.Errorf("stmap store: %s: file too short for header", name)
	}

	width := int(binary.BigEndian.Uint32(data[0:4]))
	height := int(binary.BigEndian.Uint32(data[4:8]))
	n := width * height

	expectedSize := 8 + n*4*2
	if len(data) != expectedSize {
		return nil, fmt.Errorf("stmap store: %s: expected %d bytes, got %d", name, expectedSize, len(data))
	}

	sPlane := make([]float32, n)
	tPlane := make([]float32, n)

	off := 8
	for i := 0; i < n; i++ {
		sPlane[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
	}
	for i := 0; i < n; i++ {
		tPlane[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
	}

	return &STMap{
		Name:   name,
		Width:  width,
		Height: height,
		S:      sPlane,
		T:      tPlane,
	}, nil
}

// AnimatedMeta holds the stored metadata for regenerating an animated map.
type AnimatedMeta struct {
	Generator  string             `json:"generator"`
	Params     map[string]float64 `json:"params"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	FrameCount int                `json:"frameCount"`
	FPS        int                `json:"fps"`
}

// SaveAnimatedMeta saves animated map metadata for regeneration on startup.
// Stores as JSON: {generator, params, width, height, frameCount, fps}
// Filename: {dir}/{name}.stmap-anim.json
func (s *Store) SaveAnimatedMeta(a *AnimatedSTMap, generator string, params map[string]float64) error {
	if err := ValidateName(a.Name); err != nil {
		return err
	}

	width, height := 0, 0
	if len(a.Frames) > 0 {
		width = a.Frames[0].Width
		height = a.Frames[0].Height
	}

	meta := AnimatedMeta{
		Generator:  generator,
		Params:     params,
		Width:      width,
		Height:     height,
		FrameCount: len(a.Frames),
		FPS:        a.FPS,
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("stmap store: marshal animated meta: %w", err)
	}

	path := filepath.Join(s.dir, a.Name+animatedExt)
	return os.WriteFile(path, data, 0o644)
}

// LoadAnimatedMeta reads animated map metadata from disk.
func (s *Store) LoadAnimatedMeta(name string) (*AnimatedMeta, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	path := filepath.Join(s.dir, name+animatedExt)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("stmap store: read animated meta %s: %w", name, err)
	}

	var meta AnimatedMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("stmap store: unmarshal animated meta %s: %w", name, err)
	}

	return &meta, nil
}

// Delete removes a map file (static or animated meta) from disk.
// It tries static (.stmap) first, then animated (.stmap-anim.json).
func (s *Store) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	// Try static file first.
	staticPath := filepath.Join(s.dir, name+staticExt)
	if err := os.Remove(staticPath); err == nil {
		return nil
	}

	// Try animated meta file.
	animPath := filepath.Join(s.dir, name+animatedExt)
	if err := os.Remove(animPath); err == nil {
		return nil
	}

	return ErrNotFound
}

// ListStatic returns names of all static .stmap files in the directory.
func (s *Store) ListStatic() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("stmap store: list static: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, staticExt) {
			names = append(names, strings.TrimSuffix(n, staticExt))
		}
	}
	return names, nil
}

// ListAnimated returns names of all .stmap-anim.json files in the directory.
func (s *Store) ListAnimated() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("stmap store: list animated: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, animatedExt) {
			names = append(names, strings.TrimSuffix(n, animatedExt))
		}
	}
	return names, nil
}
