package tracking

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// StudioMap holds a pre-scanned 3D reference of the studio for visual
// localization. The map is built offline from COLMAP reconstruction output
// and imported into switchframe. It contains the 3D point cloud with
// SuperPoint descriptors used for matching live frames against the map.
type StudioMap struct {
	// Points3D holds the reconstructed 3D coordinates from COLMAP.
	Points3D [][3]float64 `json:"points3d"`

	// Descriptors holds the SuperPoint feature descriptor for each 3D point.
	// Each descriptor is 256 floats (L2-normalized).
	Descriptors [][]float32 `json:"-"`

	// Intrinsics holds the reference camera calibration used during mapping.
	Intrinsics CameraIntrinsics `json:"intrinsics"`

	// Ready indicates the map has been loaded and validated.
	Ready bool `json:"-"`

	// Dir is the storage directory for this map.
	Dir string `json:"-"`
}

// descriptorDim is the dimensionality of SuperPoint descriptors.
const descriptorDim = 256

// Map file names within the map directory.
const (
	points3DFile    = "points3d.json"
	descriptorsFile = "descriptors.bin"
	intrinsicsFile  = "intrinsics.json"
)

// Sentinel errors for studio map operations.
var (
	// ErrMapEmpty indicates the studio map has no 3D points.
	ErrMapEmpty = errors.New("tracking: studio map is empty")

	// ErrDescriptorMismatch indicates descriptor count doesn't match point count.
	ErrDescriptorMismatch = errors.New("tracking: descriptor count does not match point count")

	// ErrInvalidDescriptor indicates a descriptor has wrong dimensionality.
	ErrInvalidDescriptor = errors.New("tracking: invalid descriptor dimensionality")

	// ErrMapDirMissing indicates the map directory does not exist.
	ErrMapDirMissing = errors.New("tracking: map directory does not exist")
)

// LoadStudioMap loads a studio map from a directory containing:
//   - points3d.json — array of [x,y,z] coordinates
//   - descriptors.bin — flat float32 array (N * 256)
//   - intrinsics.json — CameraIntrinsics
//
// Returns ErrMapDirMissing if the directory does not exist.
func LoadStudioMap(dir string) (*StudioMap, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMapDirMissing
		}
		return nil, fmt.Errorf("tracking: stat map dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("tracking: %s is not a directory", dir)
	}

	// Load 3D points.
	pointsPath := filepath.Join(dir, points3DFile)
	pointsData, err := os.ReadFile(pointsPath)
	if err != nil {
		return nil, fmt.Errorf("tracking: read points3d.json: %w", err)
	}

	var points [][3]float64
	if err := json.Unmarshal(pointsData, &points); err != nil {
		return nil, fmt.Errorf("tracking: parse points3d.json: %w", err)
	}
	if len(points) == 0 {
		return nil, ErrMapEmpty
	}

	// Validate points contain no NaN/Inf.
	for i, p := range points {
		for axis := 0; axis < 3; axis++ {
			if math.IsNaN(p[axis]) || math.IsInf(p[axis], 0) {
				return nil, fmt.Errorf("tracking: point %d axis %d is NaN/Inf", i, axis)
			}
		}
	}

	// Load intrinsics.
	intrinsicsPath := filepath.Join(dir, intrinsicsFile)
	intrinsicsData, err := os.ReadFile(intrinsicsPath)
	if err != nil {
		return nil, fmt.Errorf("tracking: read intrinsics.json: %w", err)
	}

	var intrinsics CameraIntrinsics
	if err := json.Unmarshal(intrinsicsData, &intrinsics); err != nil {
		return nil, fmt.Errorf("tracking: parse intrinsics.json: %w", err)
	}

	// Load descriptors (flat binary float32 array).
	descPath := filepath.Join(dir, descriptorsFile)
	descData, err := os.ReadFile(descPath)
	if err != nil {
		return nil, fmt.Errorf("tracking: read descriptors.bin: %w", err)
	}

	expectedBytes := len(points) * descriptorDim * 4 // 4 bytes per float32
	if len(descData) != expectedBytes {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d",
			ErrDescriptorMismatch, expectedBytes, len(descData))
	}

	descriptors := make([][]float32, len(points))
	for i := range points {
		desc := make([]float32, descriptorDim)
		offset := i * descriptorDim * 4
		for j := 0; j < descriptorDim; j++ {
			bits := binary.LittleEndian.Uint32(descData[offset+j*4 : offset+j*4+4])
			desc[j] = math.Float32frombits(bits)
		}
		descriptors[i] = desc
	}

	m := &StudioMap{
		Points3D:    points,
		Descriptors: descriptors,
		Intrinsics:  intrinsics,
		Ready:       true,
		Dir:         dir,
	}

	return m, nil
}

// SaveStudioMap writes a studio map to a directory, creating it if needed.
// The map must have at least one point and matching descriptors.
func SaveStudioMap(dir string, m *StudioMap) error {
	if m == nil {
		return ErrMapEmpty
	}
	if len(m.Points3D) == 0 {
		return ErrMapEmpty
	}
	if len(m.Descriptors) != len(m.Points3D) {
		return ErrDescriptorMismatch
	}

	// Validate descriptor dimensionality.
	for i, d := range m.Descriptors {
		if len(d) != descriptorDim {
			return fmt.Errorf("%w: point %d has %d dims, expected %d",
				ErrInvalidDescriptor, i, len(d), descriptorDim)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tracking: create map dir: %w", err)
	}

	// Write points3d.json.
	pointsData, err := json.MarshalIndent(m.Points3D, "", "  ")
	if err != nil {
		return fmt.Errorf("tracking: marshal points3d: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, points3DFile), pointsData, 0o644); err != nil {
		return fmt.Errorf("tracking: write points3d.json: %w", err)
	}

	// Write intrinsics.json.
	intrData, err := json.MarshalIndent(m.Intrinsics, "", "  ")
	if err != nil {
		return fmt.Errorf("tracking: marshal intrinsics: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, intrinsicsFile), intrData, 0o644); err != nil {
		return fmt.Errorf("tracking: write intrinsics.json: %w", err)
	}

	// Write descriptors.bin (flat little-endian float32).
	descBytes := make([]byte, len(m.Points3D)*descriptorDim*4)
	for i, desc := range m.Descriptors {
		offset := i * descriptorDim * 4
		for j, v := range desc {
			binary.LittleEndian.PutUint32(descBytes[offset+j*4:], math.Float32bits(v))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, descriptorsFile), descBytes, 0o644); err != nil {
		return fmt.Errorf("tracking: write descriptors.bin: %w", err)
	}

	m.Dir = dir
	m.Ready = true
	return nil
}

// NumPoints returns the number of 3D reference points in the map.
func (m *StudioMap) NumPoints() int {
	if m == nil {
		return 0
	}
	return len(m.Points3D)
}

// Validate checks the studio map for internal consistency.
func (m *StudioMap) Validate() error {
	if m == nil {
		return ErrMapEmpty
	}
	if len(m.Points3D) == 0 {
		return ErrMapEmpty
	}
	if len(m.Descriptors) != len(m.Points3D) {
		return ErrDescriptorMismatch
	}
	for i, d := range m.Descriptors {
		if len(d) != descriptorDim {
			return fmt.Errorf("%w: point %d has %d dims, expected %d",
				ErrInvalidDescriptor, i, len(d), descriptorDim)
		}
	}
	return nil
}
