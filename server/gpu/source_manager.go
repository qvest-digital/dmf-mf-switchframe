//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
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

	mu        sync.RWMutex
	sources   map[string]*gpuSourceEntry
	segEngine *SegmentationEngine // nil when AI segmentation is not configured
}

// gpuSourceEntry holds the GPU state for a single source.
type gpuSourceEntry struct {
	current atomic.Pointer[GPUFrame] // latest frame for readers

	// Preview encoder (nil if no preview configured).
	prevEnc   *PreviewEncoder
	onPreview func(data []byte, isIDR bool, pts int64)
	prevCh    chan any // platform-specific frame type (capacity 1, newest-wins)
	prevDone  chan struct{}  // closed when preview goroutine exits

	// Per-source ST map state.
	stmap     *GPUSTMap
	stmapName string
	stmapTmp  *GPUFrame // temp buffer for ST map warp

	// GPU scaling for resolution-mismatched sources (e.g., 720p → 1080p).
	scalePool *FramePool // per-source pool at source resolution
	scaleW    int        // source width that scalePool was created for
	scaleH    int        // source height

	// CPU scale buffer (fallback when GPU scaling unavailable).
	scaleBuf []byte

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

// SetSegmentationEngine wires a SegmentationEngine into the source manager.
// When set (non-nil), IngestYUV will call engine.Segment() for every source
// that has segmentation enabled. Pass nil to disable.
func (m *GPUSourceManager) SetSegmentationEngine(engine *SegmentationEngine) {
	m.mu.Lock()
	m.segEngine = engine
	m.mu.Unlock()
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
			entry.prevCh = make(chan any, 1)
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

	// Free GPU scale pool.
	if entry.scalePool != nil {
		entry.scalePool.Close()
		entry.scalePool = nil
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
	// Nil YUV is a no-op — this happens when the NVDEC zero-copy path
	// already ingested the frame via IngestGPUFrame and the switcher's
	// handleRawVideoFrame calls IngestYUV again for the same source.
	if len(yuv) == 0 {
		return
	}

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

	// Upload and optionally scale to pool dimensions.
	if err := m.uploadAndScale(entry, frame, yuv, sourceKey, w, h); err != nil {
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

	// Per-source AI segmentation (if enabled for this source).
	// Reads segEngine under RLock to avoid holding the lock during inference.
	m.mu.RLock()
	segEng := m.segEngine
	m.mu.RUnlock()
	if segEng != nil {
		// Deferred session creation: if a pending config exists but no active
		// session, create the session now with real frame dimensions.
		if segEng.HasPendingConfig(sourceKey) {
			smoothing := segEng.PendingSmoothing(sourceKey)
			if err := segEng.EnableSource(sourceKey, w, h, smoothing); err != nil {
				slog.Warn("gpu: source manager: deferred segmentation enable failed",
					"source", sourceKey, "error", err)
			} else {
				segEng.ClearPendingConfig(sourceKey)
				slog.Info("gpu: source manager: deferred segmentation enabled",
					"source", sourceKey, "size", [2]int{w, h})
			}
		}

		if segEng.IsEnabled(sourceKey) {
			gpuFrame := entry.current.Load()
			if gpuFrame != nil {
				segEng.Segment(sourceKey, gpuFrame)
				// Mask cached in session — pipeline node reads it via MaskForSource()
			}
		}
	}

	// Queue frame for preview encoding (platform-specific).
	// Metal: CPU YUV copy + CPU scale (avoids Metal command queue interleaving).
	// CUDA: GPU frame copy + GPU ScaleBilinear + NVENC (fully in VRAM).
	m.queuePreviewFrame(entry, yuv, w, h, pts)
}

// applySourceSTMap checks if a per-source ST map is assigned and applies it
// to the frame in-place using the stmapTmp buffer.
// uploadAndScale uploads CPU YUV420p to the GPU frame, applying GPU-side
// bilinear scaling when the source resolution doesn't match the pool format.
// Falls back to CPU scaling if GPU scaling fails.
func (m *GPUSourceManager) uploadAndScale(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, sourceKey string, w, h int) error {
	if w == dst.Width && h == dst.Height {
		// Matching dimensions — direct upload, no scaling.
		return Upload(m.ctx, dst, yuv, w, h)
	}

	// GPU-side scaling: upload at source resolution, then ScaleBilinear to pool size.
	// Lazy-create a per-source temp frame pool at source resolution.
	if entry.scalePool == nil || entry.scaleW != w || entry.scaleH != h {
		if entry.scalePool != nil {
			entry.scalePool.Close()
		}
		pool, err := NewFramePool(m.ctx, w, h, 2)
		if err != nil {
			// Fallback: CPU scale + upload at pool dimensions.
			return m.uploadCPUScaled(entry, dst, yuv, w, h)
		}
		entry.scalePool = pool
		entry.scaleW = w
		entry.scaleH = h
	}

	srcFrame, err := entry.scalePool.Acquire()
	if err != nil {
		return m.uploadCPUScaled(entry, dst, yuv, w, h)
	}

	if err := Upload(m.ctx, srcFrame, yuv, w, h); err != nil {
		srcFrame.Release()
		return err
	}

	if err := ScaleBilinear(m.ctx, dst, srcFrame); err != nil {
		srcFrame.Release()
		slog.Warn("gpu: GPU scale failed, falling back to CPU", "source", sourceKey, "error", err)
		return m.uploadCPUScaled(entry, dst, yuv, w, h)
	}

	srcFrame.Release()
	return nil
}

// uploadCPUScaled is the fallback when GPU scaling isn't available.
func (m *GPUSourceManager) uploadCPUScaled(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, w, h int) error {
	targetW, targetH := dst.Width, dst.Height
	scaledSize := targetW * targetH * 3 / 2
	if len(entry.scaleBuf) < scaledSize {
		entry.scaleBuf = make([]byte, scaledSize)
	}
	scaleYUV420pCPU(entry.scaleBuf[:scaledSize], targetW, targetH, yuv, w, h)
	return Upload(m.ctx, dst, entry.scaleBuf[:scaledSize], targetW, targetH)
}

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
	if err := CopyGPUFrame(frame, entry.stmapTmp); err != nil {
		slog.Warn("gpu: source manager: stmap copy back failed", "error", err)
	}
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

// CurrentFrame returns the latest GPU frame for the given source, or nil.
// The returned frame is owned by the source manager -- callers must NOT
// release it. This is a lightweight read for use cases that only need
// transient access (e.g., GPU encode from VRAM where the encoder reads
// the frame synchronously before returning).
func (m *GPUSourceManager) CurrentFrame(sourceKey string) *GPUFrame {
	m.mu.RLock()
	entry, ok := m.sources[sourceKey]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return entry.current.Load()
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

// Snapshot returns a map of source manager stats for debug/perf endpoints.
func (m *GPUSourceManager) Snapshot() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sources := make(map[string]any, len(m.sources))
	for key, entry := range m.sources {
		s := map[string]any{
			"width":       entry.width,
			"height":      entry.height,
			"has_frame":   entry.current.Load() != nil,
			"has_preview": entry.prevEnc != nil,
			"has_stmap":   entry.stmap != nil,
		}
		if entry.stmapName != "" {
			s["stmap_name"] = entry.stmapName
		}
		sources[key] = s
	}

	return map[string]any{
		"source_count": len(m.sources),
		"sources":      sources,
	}
}

// Pool returns the frame pool so callers can acquire frames for direct GPU
// operations (e.g., CopyNV12FromDevice from NVDEC decode).
func (m *GPUSourceManager) Pool() *FramePool { return m.pool }

// IngestGPUFrame stores a GPUFrame that is already in VRAM (e.g., from NVDEC
// zero-copy decode), applying per-source ST map correction and queuing preview.
// Unlike IngestYUV, this skips CPU→GPU upload entirely — the frame is already
// populated on the device.
//
// Ownership of frame transfers to the source manager (stored via atomic swap).
// If the source doesn't exist and auto-register fails, the frame is released.
func (m *GPUSourceManager) IngestGPUFrame(sourceKey string, frame *GPUFrame, pts int64) {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		entry = m.autoRegister(sourceKey, frame.Width, frame.Height)
		if entry == nil {
			frame.Release()
			return
		}
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

	// Per-source AI segmentation (if enabled for this source).
	m.mu.RLock()
	segEng := m.segEngine
	m.mu.RUnlock()
	if segEng != nil {
		if segEng.HasPendingConfig(sourceKey) {
			smoothing := segEng.PendingSmoothing(sourceKey)
			if err := segEng.EnableSource(sourceKey, frame.Width, frame.Height, smoothing); err != nil {
				slog.Warn("gpu: source manager: deferred segmentation enable failed",
					"source", sourceKey, "error", err)
			} else {
				segEng.ClearPendingConfig(sourceKey)
				slog.Info("gpu: source manager: deferred segmentation enabled",
					"source", sourceKey, "size", [2]int{frame.Width, frame.Height})
			}
		}

		if segEng.IsEnabled(sourceKey) {
			gpuFrame := entry.current.Load()
			if gpuFrame != nil {
				segEng.Segment(sourceKey, gpuFrame)
			}
		}
	}

	// Queue frame for preview encoding.
	// nil CPU buffer — CUDA preview path copies from GPU frame (device-to-device).
	m.queuePreviewFrame(entry, nil, frame.Width, frame.Height, pts)
}

// previewLoop, queuePreviewFrame, and scaleYUV420pCPU are defined in
// source_manager_preview.go (unified GPU frame copy + ScaleBilinearOn + encode).
