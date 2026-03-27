package tracking

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CalibrationStore persists per-camera lens calibrations to disk.
// Each source key maps to a CameraIntrinsics stored as a JSON file
// in the configured directory.
type CalibrationStore struct {
	dir  string
	mu   sync.RWMutex
	cals map[string]*CameraIntrinsics // sourceKey -> calibration
}

// NewCalibrationStore creates a calibration store backed by the given directory.
// It loads any existing calibration files on startup. The directory is created
// if it does not exist.
func NewCalibrationStore(dir string) (*CalibrationStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("tracking: create calibration dir: %w", err)
	}

	s := &CalibrationStore{
		dir:  dir,
		cals: make(map[string]*CameraIntrinsics),
	}

	// Load existing calibration files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("tracking: read calibration dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		sourceKey := strings.TrimSuffix(name, ".json")

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // skip unreadable files
		}

		var cal CameraIntrinsics
		if err := json.Unmarshal(data, &cal); err != nil {
			continue // skip invalid JSON
		}

		s.cals[sourceKey] = &cal
	}

	return s, nil
}

// Set stores a calibration for the given source key and persists it to disk.
func (s *CalibrationStore) Set(sourceKey string, cal CameraIntrinsics) error {
	if err := validateSourceKey(sourceKey); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cal, "", "  ")
	if err != nil {
		return fmt.Errorf("tracking: marshal calibration: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, sourceKey+".json")

	// Write atomically: write to temp file then rename.
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("tracking: write calibration: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("tracking: rename calibration: %w", err)
	}

	clone := cal // copy so caller can't mutate our stored value
	s.cals[sourceKey] = &clone

	return nil
}

// Get returns the calibration for the given source key, or nil if not found.
func (s *CalibrationStore) Get(sourceKey string) *CameraIntrinsics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cal, ok := s.cals[sourceKey]
	if !ok {
		return nil
	}

	// Return a copy so the caller can't mutate our stored value.
	clone := *cal
	clone.FocalLengthCurve = make([][2]float64, len(cal.FocalLengthCurve))
	copy(clone.FocalLengthCurve, cal.FocalLengthCurve)
	return &clone
}

// Delete removes a calibration for the given source key from memory and disk.
func (s *CalibrationStore) Delete(sourceKey string) error {
	if err := validateSourceKey(sourceKey); err != nil {
		return err
	}

	s.mu.Lock()
	_, exists := s.cals[sourceKey]
	if !exists {
		s.mu.Unlock()
		return ErrSourceNotFound
	}
	delete(s.cals, sourceKey)
	s.mu.Unlock()

	path := filepath.Join(s.dir, sourceKey+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("tracking: remove calibration file: %w", err)
	}

	return nil
}

// List returns a copy of all stored calibrations keyed by source.
func (s *CalibrationStore) List() map[string]*CameraIntrinsics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]*CameraIntrinsics, len(s.cals))
	for k, v := range s.cals {
		clone := *v
		clone.FocalLengthCurve = make([][2]float64, len(v.FocalLengthCurve))
		copy(clone.FocalLengthCurve, v.FocalLengthCurve)
		out[k] = &clone
	}
	return out
}

// validateSourceKey rejects source keys that could cause path traversal.
func validateSourceKey(key string) error {
	if key == "" || key == "." || key == ".." {
		return fmt.Errorf("tracking: invalid source key: %q", key)
	}
	if strings.ContainsAny(key, "/\\") {
		return fmt.Errorf("tracking: invalid source key: %q", key)
	}
	if filepath.Base(key) != key {
		return fmt.Errorf("tracking: invalid source key: %q", key)
	}
	return nil
}
