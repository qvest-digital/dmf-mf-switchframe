// Package stmap provides ST map (UV remap) types and processing for
// lens distortion correction, virtual camera effects, and animated
// transitions in the video processing pipeline.
package stmap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/mmaputil"
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
	Name       string
	Width      int                // frame width (set by GenerateAnimated)
	Height     int                // frame height (set by GenerateAnimated)
	Frames     []*STMap
	FPS        int
	Generator  string             // generator name (for cache key computation)
	GenParams  map[string]float64 // resolved params (for cache key computation)
	index      atomic.Int64
	frameCount atomic.Int32  // total frames, set at construction (race-free)
	processors []*Processor // pre-built per-frame processors (int32 LUTs, CPU warp)
	buildOnce  sync.Once    // guards processors initialization

	// Pre-built float32 S/T arrays for GPU upload path. Each entry is an mmap'd
	// region containing [S: w*h*float32][T: w*h*float32]. Written at cache build
	// time alongside .lut files. Zero runtime generation.
	stArrays    []stArrayEntry
}

// stArrayEntry holds mmap'd float32 S/T arrays for one animation frame.
type stArrayEntry struct {
	S    []float32 // sub-slice of backing
	T    []float32 // sub-slice of backing
	backing []byte // mmap region (for munmap)
}

// NewAnimatedSTMap creates a new animated ST map from a sequence of frames.
func NewAnimatedSTMap(name string, frames []*STMap, fps int) *AnimatedSTMap {
	a := &AnimatedSTMap{
		Name:   name,
		Frames: frames,
		FPS:    fps,
	}
	a.frameCount.Store(int32(len(frames)))
	return a
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

// AdvanceIndex increments the internal frame counter and returns the
// wrapped frame index. Use with ProcessorAt for allocation-free pipeline
// processing of animated maps. Uses the atomic frameCount set at
// construction time (race-free even before BuildProcessors is called).
func (a *AnimatedSTMap) AdvanceIndex() int {
	idx := a.index.Add(1)
	n := int(a.frameCount.Load())
	return int(idx) % n
}

// FrameCount returns the number of frames in this animation. Uses the atomic
// frameCount set at construction time (race-free).
func (a *AnimatedSTMap) FrameCount() int {
	return int(a.frameCount.Load())
}

// STArraysAt returns the pre-built float32 S and T arrays for the given frame
// index. Returns (nil, nil) if the frame index is out of range or no pre-built
// arrays are available. These arrays are mmap'd from .st cache files written
// at build time — no runtime generation.
func (a *AnimatedSTMap) STArraysAt(idx int) (s, t []float32) {
	frameCount := a.FrameCount()
	if frameCount == 0 {
		return nil, nil
	}
	wrapped := idx % frameCount

	// Pre-built mmap'd arrays (normal path after BuildProcessorsCached).
	if wrapped < len(a.stArrays) && a.stArrays[wrapped].S != nil {
		return a.stArrays[wrapped].S, a.stArrays[wrapped].T
	}

	// Fallback: Frames still in memory (before BuildProcessorsCached).
	if a.Frames != nil && wrapped < len(a.Frames) {
		return a.Frames[wrapped].S, a.Frames[wrapped].T
	}

	return nil, nil
}

// BuildProcessors pre-builds all per-frame Processors. Call this after
// construction to avoid lazy-init races and eliminate the LUT build
// latency spike (~5ms per frame) from the first animation cycle.
// Safe to call from any goroutine; uses sync.Once internally.
func (a *AnimatedSTMap) BuildProcessors() {
	a.buildOnce.Do(func() {
		a.processors = make([]*Processor, len(a.Frames))
		for i, frame := range a.Frames {
			a.processors[i] = NewProcessor(frame)
		}
	})
}

// ProcessorAt returns the precomputed Processor for the given frame index.
// Calls BuildProcessors on first access if not already called.
// Uses len(processors) for modulo (not len(Frames)) because
// BuildProcessorsCached nils the Frames slice after building.
func (a *AnimatedSTMap) ProcessorAt(idx int) *Processor {
	a.BuildProcessors()
	return a.processors[idx%len(a.processors)]
}

// BuildProcessorsCached pre-builds all per-frame Processors, writing LUT data
// to cache files in cacheDir. If cache files already exist with the correct
// size, they are mmap'd directly (instant). After building, Frames is niled
// to free the float32 source data (regenerable from generator formula).
func (a *AnimatedSTMap) BuildProcessorsCached(cacheDir string) {
	a.buildOnce.Do(func() {
		_ = os.MkdirAll(cacheDir, 0755)
		n := len(a.Frames)
		a.processors = make([]*Processor, n)
		a.stArrays = make([]stArrayEntry, n)

		for i, frame := range a.Frames {
			// Write/load int32 LUT cache (CPU warp path).
			lutPath := filepath.Join(cacheDir, fmt.Sprintf("frame_%03d.lut", i))
			p, err := NewProcessorCached(frame, lutPath)
			if err != nil {
				p = NewProcessor(frame)
			}
			a.processors[i] = p

			// Write/load float32 S/T cache (GPU upload path).
			stPath := filepath.Join(cacheDir, fmt.Sprintf("frame_%03d.st", i))
			a.stArrays[i] = loadOrWriteSTCache(stPath, frame)
		}

		// Free heap-allocated float32 source data — now served from mmap.
		a.Frames = nil
	})
}

// Close munmaps all mmap-backed processors. Safe to call multiple times.
// IMPORTANT: Close must not be called while any goroutine is using
// ProcessorAt(). In practice this is enforced by the Registry: RemoveProgram()
// is always called before Delete(), ensuring the pipeline has stopped
// referencing the processors before they are munmapped.
func (a *AnimatedSTMap) Close() error {
	var firstErr error
	for _, p := range a.processors {
		if p != nil {
			if err := p.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for i := range a.stArrays {
		if a.stArrays[i].backing != nil {
			if err := mmaputil.Free(a.stArrays[i].backing); err != nil && firstErr == nil {
				firstErr = err
			}
			a.stArrays[i] = stArrayEntry{}
		}
	}
	return firstErr
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
