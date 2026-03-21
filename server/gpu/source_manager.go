//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

// GPUSourceManager manages per-source GPU frame caching, per-source ST map
// correction, and per-source GPU preview encoding. Each source gets a
// gpuSourceEntry that holds the latest GPU frame (atomic pointer for lock-free
// reads from the pipeline goroutine) and optionally a preview encoder that
// runs in a dedicated goroutine.
//
// IngestYUV is called from the switcher's handleRawVideoFrame goroutine
// (single-threaded per source, but multiple sources call concurrently).
// GetFrame is called from the pipeline goroutine (videoProcessingLoop).
// The atomic.Pointer[GPUFrame] on current ensures these never block each other.
type GPUSourceManager struct {
	ctx    *Context
	pool   *FramePool
	stmaps SourceSTMapProvider

	mu      sync.RWMutex
	sources map[string]*gpuSourceEntry
}

// gpuSourceEntry holds the GPU state for a single source.
type gpuSourceEntry struct {
	current atomic.Pointer[GPUFrame] // latest frame for readers

	// Preview encoder (nil if no preview configured).
	prevEnc   *PreviewEncoder
	onPreview func(data []byte, isIDR bool, pts int64)
	prevCh    chan *GPUFrame // capacity 1, newest-wins non-blocking send
	prevDone  chan struct{}  // closed when preview goroutine exits

	// Per-source ST map state.
	stmap     *GPUSTMap
	stmapName string
	stmapTmp  *GPUFrame // temp buffer for ST map warp

	width, height int
}

// NewGPUSourceManager creates a new source manager.
// stmaps may be nil to disable per-source ST map correction.
func NewGPUSourceManager(ctx *Context, pool *FramePool, stmaps SourceSTMapProvider) *GPUSourceManager {
	return &GPUSourceManager{
		ctx:     ctx,
		pool:    pool,
		stmaps:  stmaps,
		sources: make(map[string]*gpuSourceEntry),
	}
}

// RegisterSource creates a GPU source entry. If preview is non-nil, a preview
// encoder and its dedicated goroutine are started. Allocates a stmapTmp frame
// for potential ST map warp operations.
func (m *GPUSourceManager) RegisterSource(sourceKey string, w, h int, preview *PreviewConfig) {
	entry := &gpuSourceEntry{
		width:  w,
		height: h,
	}

	// Allocate a temp frame for ST map warp (needs separate src/dst).
	tmpFrame, err := m.pool.Acquire()
	if err != nil {
		slog.Warn("gpu: source manager: failed to allocate stmap tmp frame",
			"source", sourceKey, "error", err)
	} else {
		entry.stmapTmp = tmpFrame
	}

	// Set up preview encoder if configured.
	if preview != nil && preview.OnPreview != nil {
		enc, encErr := NewPreviewEncoder(m.ctx, w, h,
			preview.Width, preview.Height,
			preview.Bitrate, preview.FPSNum, preview.FPSDen)
		if encErr != nil {
			slog.Warn("gpu: source manager: preview encoder failed",
				"source", sourceKey, "error", encErr)
		} else {
			entry.prevEnc = enc
			entry.onPreview = preview.OnPreview
			entry.prevCh = make(chan *GPUFrame, 1)
			entry.prevDone = make(chan struct{})
			go m.previewLoop(sourceKey, entry)
		}
	}

	m.mu.Lock()
	// Clean up any existing entry with the same key.
	if old, exists := m.sources[sourceKey]; exists {
		m.cleanupEntryLocked(old)
	}
	m.sources[sourceKey] = entry
	m.mu.Unlock()

	slog.Info("gpu: source manager: registered source",
		"source", sourceKey, "size", [2]int{w, h},
		"preview", preview != nil)
}

// RemoveSource closes the preview encoder, releases GPU resources, and
// removes the source entry.
func (m *GPUSourceManager) RemoveSource(sourceKey string) {
	m.mu.Lock()
	entry, exists := m.sources[sourceKey]
	if exists {
		delete(m.sources, sourceKey)
	}
	m.mu.Unlock()

	if !exists {
		return
	}

	m.cleanupEntryLocked(entry)
	slog.Info("gpu: source manager: removed source", "source", sourceKey)
}

// cleanupEntryLocked releases all resources owned by a source entry.
// The entry must already be removed from (or about to be replaced in) the map.
func (m *GPUSourceManager) cleanupEntryLocked(entry *gpuSourceEntry) {
	// Stop preview goroutine first.
	if entry.prevCh != nil {
		close(entry.prevCh)
		<-entry.prevDone // wait for goroutine to exit
	}

	if entry.prevEnc != nil {
		entry.prevEnc.Close()
		entry.prevEnc = nil
	}

	// Release current frame.
	if cur := entry.current.Swap(nil); cur != nil {
		cur.Release()
	}

	// Free ST map resources.
	if entry.stmap != nil {
		entry.stmap.Free()
		entry.stmap = nil
	}
	if entry.stmapTmp != nil {
		entry.stmapTmp.Release()
		entry.stmapTmp = nil
	}
}

// IngestYUV uploads a CPU YUV420p frame to GPU, optionally applies per-source
// ST map correction, stores it as the latest frame, and queues it for preview
// encoding if configured.
//
// Sources that have not been explicitly registered via RegisterSource are
// auto-registered with the provided dimensions and no preview encoder. This
// lazy initialization ensures the GPU cache is populated for all sources
// (key/layout GPUFill reads) without requiring the app layer to register
// every source at creation time.
func (m *GPUSourceManager) IngestYUV(sourceKey string, yuv []byte, w, h int, pts int64) {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		entry = m.autoRegister(sourceKey, w, h)
		if entry == nil {
			return
		}
	}

	// Acquire a GPU frame from the pool.
	frame, err := m.pool.Acquire()
	if err != nil {
		slog.Warn("gpu: source manager: pool acquire failed",
			"source", sourceKey, "error", err)
		return
	}

	// Upload CPU YUV420p to GPU NV12.
	if err := Upload(m.ctx, frame, yuv, w, h); err != nil {
		slog.Warn("gpu: source manager: upload failed",
			"source", sourceKey, "error", err)
		frame.Release()
		return
	}
	frame.PTS = pts

	// Apply per-source ST map correction if configured.
	if m.stmaps != nil {
		m.applySourceSTMap(sourceKey, entry, frame)
	}

	// Atomic swap: store new frame, release old.
	old := entry.current.Swap(frame)
	if old != nil {
		old.Release()
	}

	// Queue for preview encoding (non-blocking, newest-wins).
	if entry.prevCh != nil {
		frame.Ref() // preview goroutine will Release
		select {
		case entry.prevCh <- frame:
			// Queued successfully.
		default:
			// Channel full — drop the oldest, send newest.
			select {
			case dropped := <-entry.prevCh:
				dropped.Release()
			default:
			}
			select {
			case entry.prevCh <- frame:
			default:
				// Still can't send (race). Release the ref.
				frame.Release()
			}
		}
	}
}

// applySourceSTMap checks if a per-source ST map is assigned and applies it
// to the frame in-place using the stmapTmp buffer.
func (m *GPUSourceManager) applySourceSTMap(sourceKey string, entry *gpuSourceEntry, frame *GPUFrame) {
	name := m.stmaps.SourceMapName(sourceKey)
	if name == "" {
		// No ST map assigned. Free any cached one.
		if entry.stmap != nil {
			entry.stmap.Free()
			entry.stmap = nil
			entry.stmapName = ""
		}
		return
	}

	// Check if we need to re-upload the ST map (name changed or first time).
	if entry.stmap == nil || entry.stmapName != name {
		if entry.stmap != nil {
			entry.stmap.Free()
			entry.stmap = nil
		}
		sOrig, tOrig := m.stmaps.SourceSTArrays(sourceKey)
		if sOrig == nil || tOrig == nil {
			return
		}
		// Deep copy to prevent aliasing with registry's internal arrays.
		s := make([]float32, len(sOrig))
		t := make([]float32, len(tOrig))
		copy(s, sOrig)
		copy(t, tOrig)
		gpuMap, err := UploadSTMap(m.ctx, s, t, frame.Width, frame.Height)
		if err != nil {
			slog.Warn("gpu: source manager: stmap upload failed",
				"source", sourceKey, "error", err)
			return
		}
		entry.stmap = gpuMap
		entry.stmapName = name
	}

	// Need a temp frame to warp into (STMapWarp requires separate src/dst).
	if entry.stmapTmp == nil {
		return
	}

	entry.stmapTmp.PTS = frame.PTS
	if err := STMapWarp(m.ctx, entry.stmapTmp, frame, entry.stmap); err != nil {
		slog.Warn("gpu: source manager: stmap warp failed",
			"source", sourceKey, "error", err)
		return
	}

	// Copy warped result back to the original frame.
	copyGPUFrameNV12(frame, entry.stmapTmp)
}

// autoRegister creates a new source entry with no preview encoder. Called
// from IngestYUV when the source has not been explicitly registered. This
// ensures GPU cache is populated for GPUFill reads from key/layout nodes.
// Returns the new entry, or nil if allocation failed.
func (m *GPUSourceManager) autoRegister(sourceKey string, w, h int) *gpuSourceEntry {
	m.mu.Lock()
	// Double-check under write lock — another goroutine may have registered
	// between the RUnlock and this Lock.
	if entry, ok := m.sources[sourceKey]; ok {
		m.mu.Unlock()
		return entry
	}

	entry := &gpuSourceEntry{
		width:  w,
		height: h,
	}

	// Allocate a temp frame for ST map warp.
	tmpFrame, err := m.pool.Acquire()
	if err != nil {
		slog.Warn("gpu: source manager: auto-register: stmap tmp alloc failed",
			"source", sourceKey, "error", err)
		// Still usable without stmapTmp — just won't do per-source ST map.
	} else {
		entry.stmapTmp = tmpFrame
	}

	m.sources[sourceKey] = entry
	m.mu.Unlock()

	slog.Info("gpu: source manager: auto-registered source",
		"source", sourceKey, "size", [2]int{w, h})
	return entry
}

// GetFrame returns the latest GPU frame for a source, incrementing its
// reference count. The caller must call Release() when done. Returns nil
// if the source is not found or has no frame yet.
//
// Uses a CAS-like retry loop to prevent a use-after-free race: between
// Load() and Ref(), IngestYUV can Swap and Release the old frame. The
// loop verifies the frame is still current after Ref(); if not, it
// releases and retries with the new frame.
func (m *GPUSourceManager) GetFrame(sourceKey string) *GPUFrame {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		return nil
	}

	for {
		frame := entry.current.Load()
		if frame == nil {
			return nil
		}

		frame.Ref()
		// Verify the frame is still the current one (not swapped out).
		if entry.current.Load() == frame {
			return frame // Success — we hold a valid ref.
		}
		// Frame was swapped between Load and Ref. Release our ref and retry.
		frame.Release()
	}
}

// Close removes all sources and releases all GPU resources.
func (m *GPUSourceManager) Close() {
	m.mu.Lock()
	sources := m.sources
	m.sources = make(map[string]*gpuSourceEntry)
	m.mu.Unlock()

	for _, entry := range sources {
		m.cleanupEntryLocked(entry)
	}
}

// CopyGPUFrame copies NV12 data from src to dst using unified memory memcpy.
// Both frames must have the same dimensions and pitch. No-ops with an error
// log if dimensions mismatch.
func CopyGPUFrame(dst, src *GPUFrame) {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		slog.Error("CopyGPUFrame: dimension mismatch",
			"dst", fmt.Sprintf("%dx%d p=%d", dst.Width, dst.Height, dst.Pitch),
			"src", fmt.Sprintf("%dx%d p=%d", src.Width, src.Height, src.Pitch))
		return
	}
	copyGPUFrameNV12(dst, src)
}

// previewLoop is the per-source preview encoding goroutine. It reads frames
// from prevCh, encodes them, and calls the onPreview callback. Exits when
// prevCh is closed.
func (m *GPUSourceManager) previewLoop(sourceKey string, entry *gpuSourceEntry) {
	defer close(entry.prevDone)

	forceIDR := true // force first frame to be IDR
	for frame := range entry.prevCh {
		pts := frame.PTS // capture before Release
		data, isIDR, err := entry.prevEnc.Encode(frame, forceIDR)
		frame.Release()
		if err != nil {
			slog.Warn("gpu: source manager: preview encode failed",
				"source", sourceKey, "error", err)
			continue
		}
		forceIDR = false
		if len(data) > 0 && entry.onPreview != nil {
			entry.onPreview(data, isIDR, pts)
		}
	}
}
