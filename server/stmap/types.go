// Package stmap provides ST map (UV remap) types and processing for
// lens distortion correction, virtual camera effects, and animated
// transitions in the video processing pipeline.
package stmap

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
)

var (
	// ErrInvalidDimensions is returned when width or height are non-positive or odd.
	ErrInvalidDimensions = errors.New("stmap: invalid dimensions (must be positive and even)")
	// ErrInvalidName is returned when a map name contains invalid characters.
	ErrInvalidName = errors.New("stmap: invalid name")
	// ErrNotFound is returned when a map is not found in the registry.
	ErrNotFound = errors.New("stmap: not found")
	// ErrAlreadyExists is returned when a map with the same name already exists.
	ErrAlreadyExists = errors.New("stmap: already exists")
)

// STMap holds a 2D UV remap (ST map) where each pixel stores the
// normalized source coordinate it should sample from. S is the
// horizontal coordinate (0.0–1.0) and T is the vertical coordinate
// (0.0–1.0). Both slices have length Width*Height in row-major order.
type STMap struct {
	Name   string
	Width  int
	Height int
	S      []float32 // horizontal coords, normalized 0.0–1.0
	T      []float32 // vertical coords, normalized 0.0–1.0
}

// NewSTMap creates an empty ST map with zero-initialized coordinates.
// Width and height must be positive and even (required for YUV420 processing).
func NewSTMap(name string, width, height int) (*STMap, error) {
	if width <= 0 || height <= 0 || width%2 != 0 || height%2 != 0 {
		return nil, ErrInvalidDimensions
	}
	n := width * height
	return &STMap{
		Name:   name,
		Width:  width,
		Height: height,
		S:      make([]float32, n),
		T:      make([]float32, n),
	}, nil
}

// Identity creates an identity ST map where each pixel maps to itself.
// Pixel-center sampling is used: S = (x+0.5)/width, T = (y+0.5)/height.
func Identity(width, height int) *STMap {
	n := width * height
	s := make([]float32, n)
	t := make([]float32, n)
	fw := float32(width)
	fh := float32(height)
	for y := 0; y < height; y++ {
		row := y * width
		ty := (float32(y) + 0.5) / fh
		for x := 0; x < width; x++ {
			idx := row + x
			s[idx] = (float32(x) + 0.5) / fw
			t[idx] = ty
		}
	}
	return &STMap{
		Name:   "identity",
		Width:  width,
		Height: height,
		S:      s,
		T:      t,
	}
}

// AnimatedSTMap holds a sequence of ST map frames for animated effects.
type AnimatedSTMap struct {
	Name   string
	Frames []*STMap
	FPS    int
	index  atomic.Int64
}

// NewAnimatedSTMap creates a new animated ST map from a sequence of frames.
func NewAnimatedSTMap(name string, frames []*STMap, fps int) *AnimatedSTMap {
	return &AnimatedSTMap{
		Name:   name,
		Frames: frames,
		FPS:    fps,
	}
}

// FrameAt returns the frame at the given index, wrapping around with modulo.
func (a *AnimatedSTMap) FrameAt(idx int) *STMap {
	return a.Frames[idx%len(a.Frames)]
}

// Advance increments the internal frame counter and returns the current frame.
func (a *AnimatedSTMap) Advance() *STMap {
	idx := a.index.Add(1)
	return a.Frames[int(idx)%len(a.Frames)]
}

// CurrentFrame returns the current frame without advancing the counter.
func (a *AnimatedSTMap) CurrentFrame() *STMap {
	idx := a.index.Load()
	return a.Frames[int(idx)%len(a.Frames)]
}

// ValidateName rejects names that could cause path traversal or are otherwise
// invalid. Follows the same pattern as stinger/store.go:validateName.
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidName
	}
	// Ensure the cleaned name matches the original (catches things like "a/../b").
	if filepath.Base(name) != name {
		return ErrInvalidName
	}
	return nil
}
