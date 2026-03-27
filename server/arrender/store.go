package arrender

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed testdata/podium.glb testdata/clock.glb testdata/ltstand.glb
var bundledAssets embed.FS

// acceptedExtensions maps file extensions to canonical format names.
var acceptedExtensions = map[string]string{
	".glb":  "glb",
	".gltf": "gltf",
	".usd":  "usd",
	".usda": "usda",
	".usdc": "usdc",
	".usdz": "usdz",
	".hdr":  "hdr",
	".exr":  "exr",
}

// AssetInfo holds metadata for a stored 3D asset.
type AssetInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Filename  string `json:"filename"`
	Format    string `json:"format"` // "glb", "gltf", "usd", "usda", "usdz", "hdr", "exr"
	ByteSize  int64  `json:"byteSize"`
	CreatedAt int64  `json:"createdAt"` // Unix milliseconds
}

// AssetStore manages file-based CRUD for 3D assets. Metadata is persisted
// as JSON in assets.json alongside the asset files.
type AssetStore struct {
	dir       string
	mu        sync.RWMutex
	assets    map[string]*AssetInfo
	maxAssets int
	metaPath  string
}

// NewAssetStore creates an asset store backed by the given directory.
// If assets.json exists, metadata is loaded from it. maxAssets limits
// the number of stored assets; values <= 0 default to 64.
func NewAssetStore(dir string, maxAssets int) (*AssetStore, error) {
	if maxAssets <= 0 {
		maxAssets = 64
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create asset dir: %w", err)
	}

	s := &AssetStore{
		dir:       dir,
		assets:    make(map[string]*AssetInfo),
		maxAssets: maxAssets,
		metaPath:  filepath.Join(dir, "assets.json"),
	}

	data, err := os.ReadFile(s.metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read assets file: %w", err)
	}

	var assets []*AssetInfo
	if err := json.Unmarshal(data, &assets); err != nil {
		return nil, fmt.Errorf("parse assets file: %w", err)
	}
	for _, a := range assets {
		s.assets[a.ID] = a
	}

	return s, nil
}

// Upload stores a new asset. The filename must have an accepted extension.
// Returns ErrInvalidAsset for bad names/extensions and ErrTooManyAssets
// if the store is full.
func (s *AssetStore) Upload(name, filename string, data []byte) (*AssetInfo, error) {
	if err := validateAssetName(name); err != nil {
		return nil, err
	}
	if err := validateAssetFilename(filename); err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(filename))
	format, ok := acceptedExtensions[ext]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported extension %q", ErrInvalidAsset, ext)
	}

	id, err := generateAssetID()
	if err != nil {
		return nil, fmt.Errorf("generate asset ID: %w", err)
	}

	// Build a unique on-disk filename: id + original extension.
	diskFilename := id + ext

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.assets) >= s.maxAssets {
		return nil, ErrTooManyAssets
	}

	// Write asset file.
	assetPath := filepath.Join(s.dir, diskFilename)
	if err := os.WriteFile(assetPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write asset file: %w", err)
	}

	info := &AssetInfo{
		ID:        id,
		Name:      name,
		Filename:  diskFilename,
		Format:    format,
		ByteSize:  int64(len(data)),
		CreatedAt: time.Now().UnixMilli(),
	}
	s.assets[id] = info

	if err := s.saveLocked(); err != nil {
		// Rollback: remove asset file and map entry.
		delete(s.assets, id)
		_ = os.Remove(assetPath)
		return nil, fmt.Errorf("save metadata: %w", err)
	}

	cp := *info
	return &cp, nil
}

// Get returns a copy of the asset info for the given ID.
// Returns ErrAssetNotFound if no asset with that ID exists.
func (s *AssetStore) Get(id string) (*AssetInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a, ok := s.assets[id]
	if !ok {
		return nil, ErrAssetNotFound
	}
	cp := *a
	return &cp, nil
}

// List returns all assets sorted by CreatedAt ascending (oldest first).
func (s *AssetStore) List() []*AssetInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*AssetInfo, 0, len(s.assets))
	for _, a := range s.assets {
		cp := *a
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result
}

// Delete removes an asset by ID, deletes the file from disk, and
// persists the metadata change. Returns ErrAssetNotFound if the
// asset does not exist.
func (s *AssetStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.assets[id]
	if !ok {
		return ErrAssetNotFound
	}

	filename := a.Filename
	delete(s.assets, id)

	if err := s.saveLocked(); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}

	// Best-effort file deletion; metadata is already removed.
	if filename != "" {
		_ = os.Remove(filepath.Join(s.dir, filename))
	}
	return nil
}

// FilePath returns the absolute path to the asset file on disk.
// Returns ErrAssetNotFound if the asset does not exist.
func (s *AssetStore) FilePath(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a, ok := s.assets[id]
	if !ok {
		return "", ErrAssetNotFound
	}
	return filepath.Join(s.dir, a.Filename), nil
}

// Close is a no-op; metadata is persisted on each mutation.
func (s *AssetStore) Close() error {
	return nil
}

// LoadBundledAssets copies embedded test GLB files into the store
// if they don't already exist (matched by original filename). Called
// once on startup to seed the asset library with sample models.
func (s *AssetStore) LoadBundledAssets() error {
	entries, err := fs.ReadDir(bundledAssets, "testdata")
	if err != nil {
		return fmt.Errorf("read bundled assets: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		// Skip if an asset with this original filename already exists.
		origName := e.Name()
		if s.hasBundledFile(origName) {
			continue
		}

		data, err := fs.ReadFile(bundledAssets, "testdata/"+origName)
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(origName, filepath.Ext(origName))
		if _, err := s.Upload(name, origName, data); err != nil {
			return fmt.Errorf("load bundled asset %s: %w", origName, err)
		}
	}
	return nil
}

// hasBundledFile checks if an asset with the given original filename
// (before ID prefix) already exists in the store. The store renames files
// to id+ext on disk, so we check the Name field instead.
func (s *AssetStore) hasBundledFile(origFilename string) bool {
	name := strings.TrimSuffix(origFilename, filepath.Ext(origFilename))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.assets {
		if a.Name == name {
			return true
		}
	}
	return false
}

// saveLocked writes the current asset metadata to disk atomically
// (temp file + fsync + rename). Must be called with s.mu held.
func (s *AssetStore) saveLocked() error {
	assets := make([]*AssetInfo, 0, len(s.assets))
	for _, a := range s.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].CreatedAt < assets[j].CreatedAt
	})

	data, err := json.MarshalIndent(assets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal assets: %w", err)
	}

	tmpFile, err := os.CreateTemp(s.dir, "assets-*.tmp")
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
	if err := os.Rename(tmpPath, s.metaPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// validateAssetName rejects empty names and names with path traversal characters.
func validateAssetName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidAsset)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%w: name contains path separators", ErrInvalidAsset)
	}
	return nil
}

// validateAssetFilename rejects filenames that could cause path traversal.
func validateAssetFilename(filename string) error {
	if filename == "" || filename == "." || filename == ".." {
		return fmt.Errorf("%w: invalid filename", ErrInvalidAsset)
	}
	if strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("%w: filename contains path separators", ErrInvalidAsset)
	}
	cleaned := filepath.Base(filepath.Clean(filename))
	if cleaned != filename {
		return fmt.Errorf("%w: filename failed path traversal check", ErrInvalidAsset)
	}
	return nil
}

// generateAssetID creates a random hex identifier (16 bytes = 32 hex chars).
func generateAssetID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
