//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/internal/video"
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

	mu               sync.RWMutex
	sources          map[string]*gpuSourceEntry
	segEngine        Segmenter                // nil when AI segmentation is not configured
	sourceColorGrade SourceColorGradeProvider // nil when per-source color grading is not configured
}

// cpuFrameCache holds a cached CPU YUV420 frame for lock-free reads
// by the playout live source tap (ReadCachedFrame).
type cpuFrameCache struct {
	yuv  []byte
	w, h int
	pts  int64
}

// gpuSourceEntry holds the GPU state for a single source.
type gpuSourceEntry struct {
	// curMu serializes every publish (Swap+Release) of current with every
	// read-that-takes-a-ref (Load+Ref). A lock-free Load -> Ref -> recheck
	// loop is NOT safe with a pool that recycles frames at refcount zero:
	// between the reader's Load and Ref the publisher can Swap and Release
	// the frame to the pool, after which the reader's Ref resurrects a dead
	// refcount and its compensating Release double-inserts the frame into
	// the pool free list (frame aliasing / VRAM corruption). Holding curMu
	// across Load+Ref guarantees the publisher's reference is still alive
	// when the reader takes its own. Critical sections are a few
	// instructions; no GPU work runs under this lock.
	curMu   sync.Mutex
	current atomic.Pointer[GPUFrame]      // latest frame for readers
	lastCPU atomic.Pointer[cpuFrameCache] // cached CPU YUV for playout tap

	// cpuCacheWanted gates population of lastCPU. Set by ReadCachedFrame
	// (playout live-source tap) and propagated by AliasSource. Without a
	// reader, IngestYUV skips the per-frame full-frame allocation+copy
	// (~3.1 MB at 1080p) the cache would otherwise cost.
	cpuCacheWanted atomic.Bool

	// useMu serializes use of the per-entry GPU resources below (scalePool,
	// nv12Tmp, stmap, stmapTmp, colorLUT*, scaleBuf) between the ingest
	// paths and cleanupEntry. Ingest is single-producer per source, so
	// holding it for the duration of an ingest adds no contention; cleanup
	// acquires it once to wait out any in-flight ingest before freeing.
	useMu sync.Mutex
	// closed is set by cleanupEntry (under useMu) when the entry has been
	// removed/replaced. Ingest and alias paths drop their frames once set,
	// so nothing is published into (and leaked with) an orphaned entry and
	// no freed GPU resource is recreated or reused.
	closed atomic.Bool

	// Preview encoder (nil if no preview configured).
	prevEnc   *PreviewEncoder
	onPreview func(data []byte, isIDR bool, pts int64)
	prevCh    chan any      // platform-specific frame type (capacity 1, newest-wins)
	prevDone  chan struct{} // closed when preview goroutine exits

	// Per-source ST map state.
	stmap     *GPUSTMap
	stmapName string
	stmapTmp  *GPUFrame // temp buffer for ST map warp

	// Per-source color grade LUT (cached on GPU device memory).
	colorLUTDev  uintptr // CUDA device pointer for the uploaded 3D LUT
	colorLUTGen  uint64  // last-seen generation counter (0 = no LUT uploaded)
	colorLUTSize int     // cube dimension (e.g., 33)

	// GPU scaling for resolution-mismatched sources (e.g., 720p → 1080p).
	scalePool *FramePool // per-source pool at source resolution (format matches main pool)
	scaleW    int        // source width that scalePool was created for
	scaleH    int        // source height

	// NV12 temp pool for CUDA 8-bit→P210 conversion (Upload NV12 → ConvertNV12ToP210).
	// Not used on Metal (UploadYUV420PToP210 writes directly to unified memory).
	nv12Tmp  *FramePool
	nv12TmpW int
	nv12TmpH int

	// CPU scale buffer (fallback when GPU scaling unavailable).
	scaleBuf []byte

	width, height int

	// recent holds the last published frames, oldest first, each with a
	// reference of its own, guarded by curMu. The program is read behind
	// the newest frame (clock.ReaderDelayGrains), so the frame a tick shows
	// is usually one of these rather than current (refAt).
	recent [recentFrames]*GPUFrame
}

// recentFrames is how many published frames each source keeps for refAt:
// the reader delay plus a frame of slack.
const recentFrames = 4

// refAt returns the entry's frame with the given PTS, with an extra
// reference the caller must Release, or nil when none of the recent frames
// has it.
func refAt(entry *gpuSourceEntry, pts int64) *GPUFrame {
	entry.curMu.Lock()
	defer entry.curMu.Unlock()
	for i := len(entry.recent) - 1; i >= 0; i-- {
		if f := entry.recent[i]; f != nil && f.PTS == pts {
			f.Ref()
			return f
		}
	}
	return nil
}

// refCurrent returns the entry's current frame with an extra reference taken
// under curMu, or nil if the entry has no frame. The caller must Release the
// returned frame. See the curMu field comment for why the lock is required.
func refCurrent(entry *gpuSourceEntry) *GPUFrame {
	entry.curMu.Lock()
	frame := entry.current.Load()
	if frame != nil {
		frame.Ref()
	}
	entry.curMu.Unlock()
	return frame
}

// publishCurrent swaps frame into entry.current under curMu and releases the
// previously published frame. Passing nil drains the entry. Ownership of
// frame transfers to the entry. The old frame's Release runs outside the
// lock: any reader that Ref'd it did so under curMu before the swap, while
// our reference was still alive, so the release here can never free a frame
// a reader is still acquiring.
func publishCurrent(entry *gpuSourceEntry, frame *GPUFrame) {
	entry.curMu.Lock()
	old := entry.current.Swap(frame)
	var evicted []*GPUFrame
	if frame == nil {
		for i, f := range entry.recent {
			if f != nil {
				evicted = append(evicted, f)
				entry.recent[i] = nil
			}
		}
	} else {
		if f := entry.recent[0]; f != nil {
			evicted = append(evicted, f)
		}
		copy(entry.recent[:], entry.recent[1:])
		frame.Ref()
		entry.recent[len(entry.recent)-1] = frame
	}
	entry.curMu.Unlock()
	if old != nil {
		old.Release()
	}
	for _, f := range evicted {
		f.Release()
	}
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

// SetSegmenter wires a Segmenter into the source manager.
// When set (non-nil), IngestYUV will call seg.Segment() for every source
// that has segmentation enabled. Pass nil to disable.
func (m *GPUSourceManager) SetSegmenter(seg Segmenter) {
	m.mu.Lock()
	m.segEngine = seg
	m.mu.Unlock()
}

// SetSourceColorGrade wires a per-source color grade provider into the source
// manager. When set (non-nil), IngestYUV and IngestGPUFrame will apply per-source
// 3D LUT color grading after ST map correction. Pass nil to disable.
func (m *GPUSourceManager) SetSourceColorGrade(provider SourceColorGradeProvider) {
	m.mu.Lock()
	m.sourceColorGrade = provider
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

	// Swap the new entry in under the lock, then clean up the old one OUTSIDE
	// the lock. cleanupEntry closes prevCh and blocks on <-prevDone to
	// join the preview goroutine; doing that under m.mu would stall every
	// concurrent IngestYUV/GetFrame/CurrentFrame (all take m.mu.RLock) for the
	// duration of the join + encoder Close, and would deadlock if the preview
	// callback ever re-entered the source manager. Mirror RemoveSource.
	m.mu.Lock()
	old, hadOld := m.sources[sourceKey]
	m.sources[sourceKey] = entry
	m.mu.Unlock()

	if hadOld {
		m.cleanupEntry(old)
	}

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

	m.cleanupEntry(entry)
	slog.Info("gpu: source manager: removed source", "source", sourceKey)
}

// cleanupEntry releases all resources owned by a source entry. It runs
// WITHOUT m.mu held (closing the preview goroutine under m.mu would stall
// all sources and can deadlock on re-entrant preview callbacks). The entry
// must already be removed from (or replaced in) the map.
func (m *GPUSourceManager) cleanupEntry(entry *gpuSourceEntry) {
	// Mark the entry closed and wait for any in-flight ingest to drain.
	// Ingest paths hold useMu for their full duration, so after this block
	// no goroutine can touch the GPU resources freed below (previously
	// cleanup raced with IngestYUV's lock-free use of scalePool/nv12Tmp/
	// stmap/colorLUT — GPU use-after-free) and any frame the in-flight
	// ingest published has been swapped into entry.current, where the
	// publishCurrent(entry, nil) below releases it (no pool-frame leak).
	entry.useMu.Lock()
	entry.closed.Store(true)
	entry.useMu.Unlock()

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
	publishCurrent(entry, nil)

	// Free GPU scale pool.
	if entry.scalePool != nil {
		entry.scalePool.Close()
		entry.scalePool = nil
	}

	// Free NV12 temp pool (CUDA 8-bit→P210 conversion).
	if entry.nv12Tmp != nil {
		entry.nv12Tmp.Close()
		entry.nv12Tmp = nil
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

	// Free per-source color grade LUT.
	if entry.colorLUTDev != 0 {
		FreeLUT3D(entry.colorLUTDev)
		entry.colorLUTDev = 0
		entry.colorLUTGen = 0
		entry.colorLUTSize = 0
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

	// Hold the entry's use-lock for the whole ingest: the upload/warp/grade
	// paths below use per-entry GPU resources (scalePool, nv12Tmp, stmap,
	// stmapTmp, colorLUT) that cleanupEntry frees. cleanupEntry acquires
	// useMu before freeing, so an in-flight ingest finishes first; once the
	// entry is closed the frame is dropped (the source was removed).
	entry.useMu.Lock()
	defer entry.useMu.Unlock()
	if entry.closed.Load() {
		return
	}

	// Cache CPU YUV for ReadCachedFrame (playout live source tap) — only
	// when a reader has shown interest. Unconditionally allocating and
	// copying a full frame per ingest (~3.1 MB at 1080p) is sustained GC
	// pressure for a cache that is rarely read.
	// Done before GPU upload to avoid copying GPU-modified data back.
	// Always allocate a fresh cpuFrameCache because the previous one may
	// still be referenced by a concurrent ReadCachedFrame caller.
	if entry.cpuCacheWanted.Load() {
		cached := &cpuFrameCache{
			yuv: make([]byte, len(yuv)),
			w:   w,
			h:   h,
			pts: pts,
		}
		copy(cached.yuv, yuv)
		entry.lastCPU.Store(cached)
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

	// Apply per-source color grade LUT if configured.
	m.mu.RLock()
	colorGrade := m.sourceColorGrade
	m.mu.RUnlock()
	if colorGrade != nil {
		m.applySourceColorGrade(sourceKey, entry, frame, colorGrade)
	}

	// Publish: store new frame, release old.
	publishCurrent(entry, frame)

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
			if gpuFrame := refCurrent(entry); gpuFrame != nil {
				segEng.Segment(sourceKey, gpuFrame)
				// Mask cached in session — pipeline node reads it via MaskForSource()
				gpuFrame.Release()
			}
		}
	}

	// Queue frame for preview encoding (platform-specific).
	// Metal: CPU YUV copy + CPU scale (avoids Metal command queue interleaving).
	// CUDA: GPU frame copy + GPU ScaleBilinear + NVENC (fully in VRAM).
	m.queuePreviewFrame(entry, yuv, w, h, pts)
}

// uploadAndScale uploads CPU YUV420p to the GPU frame, applying GPU-side
// bilinear scaling when the source resolution doesn't match the pool format.
// Dispatches to format-specific paths based on the pool's pixel format.
func (m *GPUSourceManager) uploadAndScale(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, sourceKey string, w, h int) error {
	if m.pool.Format() == GPUFormatP210 {
		return m.uploadAndScaleP210(entry, dst, yuv, sourceKey, w, h)
	}
	return m.uploadAndScaleNV12(entry, dst, yuv, sourceKey, w, h)
}

// uploadAndScaleNV12 handles the NV12 upload path: direct Upload for matching
// dimensions, GPU ScaleBilinear for mismatched, CPU fallback if GPU fails.
func (m *GPUSourceManager) uploadAndScaleNV12(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, sourceKey string, w, h int) error {
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

// uploadAndScaleP210 handles the P210 upload path. Detects whether the input
// is YUV420P 8-bit (w*h*3/2 bytes) or YUV422P10LE (w*h*4 bytes) and routes
// to the appropriate upload function. The source decoder may upconvert
// YUV420P → YUV422P10LE when the pipeline is in professional mode.
func (m *GPUSourceManager) uploadAndScaleP210(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, sourceKey string, w, h int) error {
	// Detect input format: YUV422P10LE (w*h*4) vs YUV420P (w*h*3/2).
	expected10bit := video.YUV422_10bitFrameSize(w, h)
	is10bitInput := len(yuv) >= expected10bit

	if w == dst.Width && h == dst.Height {
		// Same resolution: direct upload.
		if is10bitInput {
			return Upload10(m.ctx, dst, yuv, w, h)
		}
		// 8-bit YUV420P → P210. Try unified-memory direct path first (Metal).
		// On CUDA, fall through to NV12 temp + convert path.
		if err := UploadYUV420PToP210(m.ctx, dst, yuv, w, h); err == nil {
			return nil
		}
		// CUDA fallback: Upload to NV12 temp, then ConvertNV12ToP210.
		return m.uploadNV12ThenConvertP210(entry, dst, yuv, w, h)
	}

	// Different resolution: upload to P210 temp, then scale P210→P210.
	// Lazy-create P210 scale pool at source resolution.
	if entry.scalePool == nil || entry.scaleW != w || entry.scaleH != h {
		if entry.scalePool != nil {
			entry.scalePool.Close()
		}
		pool, err := NewP210FramePool(m.ctx, w, h, 2)
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

	var uploadErr error
	if is10bitInput {
		uploadErr = Upload10(m.ctx, srcFrame, yuv, w, h)
	} else {
		// 8-bit YUV420P → P210 temp frame.
		uploadErr = UploadYUV420PToP210(m.ctx, srcFrame, yuv, w, h)
		if uploadErr != nil {
			// CUDA fallback: Upload NV12, then convert to a fresh P210 temp.
			srcFrame.Release()
			return m.uploadNV12ScaleThenConvertP210(entry, dst, yuv, sourceKey, w, h)
		}
	}
	if uploadErr != nil {
		srcFrame.Release()
		return uploadErr
	}

	if err := ScaleBilinearP210(m.ctx, dst, srcFrame); err != nil {
		srcFrame.Release()
		slog.Warn("gpu: GPU P210 scale failed, falling back to CPU", "source", sourceKey, "error", err)
		return m.uploadCPUScaled(entry, dst, yuv, w, h)
	}

	srcFrame.Release()
	return nil
}

// uploadNV12ThenConvertP210 handles same-resolution 8-bit YUV420P → P210 on CUDA
// where unified-memory UploadYUV420PToP210 is unavailable. Two steps:
// 1. Upload YUV420P → NV12 temp frame
// 2. ConvertNV12ToP210 → destination P210 frame
func (m *GPUSourceManager) uploadNV12ThenConvertP210(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, w, h int) error {
	// Need an NV12 temp frame at same dimensions.
	if entry.nv12Tmp == nil || entry.nv12TmpW != w || entry.nv12TmpH != h {
		if entry.nv12Tmp != nil {
			entry.nv12Tmp.Close()
		}
		pool, err := NewFramePool(m.ctx, w, h, 1)
		if err != nil {
			return fmt.Errorf("gpu: NV12 tmp pool failed: %w", err)
		}
		entry.nv12Tmp = pool
		entry.nv12TmpW = w
		entry.nv12TmpH = h
	}
	tmp, err := entry.nv12Tmp.Acquire()
	if err != nil {
		return fmt.Errorf("gpu: NV12 tmp acquire: %w", err)
	}
	defer tmp.Release()

	if err := Upload(m.ctx, tmp, yuv, w, h); err != nil {
		return fmt.Errorf("gpu: NV12 upload: %w", err)
	}
	return ConvertNV12ToP210(m.ctx, dst, tmp)
}

// uploadNV12ScaleThenConvertP210 handles different-resolution 8-bit YUV420P → P210
// on CUDA. Three steps:
// 1. Upload YUV420P → NV12 temp frame at source resolution
// 2. ScaleNV12ToP210 → destination P210 frame at pool resolution (combined scale+convert)
func (m *GPUSourceManager) uploadNV12ScaleThenConvertP210(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, sourceKey string, w, h int) error {
	if entry.nv12Tmp == nil || entry.nv12TmpW != w || entry.nv12TmpH != h {
		if entry.nv12Tmp != nil {
			entry.nv12Tmp.Close()
		}
		pool, err := NewFramePool(m.ctx, w, h, 1)
		if err != nil {
			return m.uploadCPUScaled(entry, dst, yuv, w, h)
		}
		entry.nv12Tmp = pool
		entry.nv12TmpW = w
		entry.nv12TmpH = h
	}
	tmp, err := entry.nv12Tmp.Acquire()
	if err != nil {
		return m.uploadCPUScaled(entry, dst, yuv, w, h)
	}
	defer tmp.Release()

	if err := Upload(m.ctx, tmp, yuv, w, h); err != nil {
		return fmt.Errorf("gpu: NV12 upload for scale: %w", err)
	}
	return ScaleNV12ToP210(m.ctx, dst, tmp)
}

// uploadCPUScaled is the fallback when GPU scaling isn't available.
// Format-aware: uses UploadYUV420PToP210 when the pool is P210.
func (m *GPUSourceManager) uploadCPUScaled(entry *gpuSourceEntry, dst *GPUFrame, yuv []byte, w, h int) error {
	targetW, targetH := dst.Width, dst.Height
	scaledSize := video.YUV420FrameSize(targetW, targetH)
	if len(entry.scaleBuf) < scaledSize {
		entry.scaleBuf = make([]byte, scaledSize)
	}
	scaleYUV420pCPU(entry.scaleBuf[:scaledSize], targetW, targetH, yuv, w, h)

	if m.pool.Format() == GPUFormatP210 {
		if err := UploadYUV420PToP210(m.ctx, dst, entry.scaleBuf[:scaledSize], targetW, targetH); err == nil {
			return nil
		}
		// CUDA fallback: Upload NV12, then convert to P210.
		return m.uploadNV12ThenConvertP210(entry, dst, entry.scaleBuf[:scaledSize], targetW, targetH)
	}
	return Upload(m.ctx, dst, entry.scaleBuf[:scaledSize], targetW, targetH)
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
	// Use the format-aware dispatch so P210 (10-bit 4:2:2) frames get
	// routed to STMapWarpP210Map. Calling STMapWarp directly here would
	// run the NV12 kernel on P210 memory, which produces green-tinted
	// garbage and can crash the pipeline downstream.
	if err := GPUSTMapWarpDispatch(m.ctx, entry.stmapTmp, frame, entry.stmap); err != nil {
		slog.Warn("gpu: source manager: stmap warp failed",
			"source", sourceKey, "error", err)
		return
	}

	// Copy warped result back to the original frame.
	if err := CopyGPUFrame(frame, entry.stmapTmp); err != nil {
		slog.Warn("gpu: source manager: stmap copy back failed", "error", err)
	}
}

// applySourceColorGrade checks if a per-source color grade LUT is assigned and
// applies it to the frame in-place. Uses a generation counter to avoid redundant
// GPU LUT uploads — the LUT is only re-uploaded when the generation changes.
func (m *GPUSourceManager) applySourceColorGrade(sourceKey string, entry *gpuSourceEntry, frame *GPUFrame, provider SourceColorGradeProvider) {
	gen := provider.SourceLUTGen(sourceKey)
	if gen == 0 {
		// No LUT assigned. Free any cached one.
		if entry.colorLUTDev != 0 {
			FreeLUT3D(entry.colorLUTDev)
			entry.colorLUTDev = 0
			entry.colorLUTGen = 0
			entry.colorLUTSize = 0
		}
		return
	}

	// Check if we need to upload a new LUT (generation changed or first time).
	if entry.colorLUTDev == 0 || gen != entry.colorLUTGen {
		// Free old LUT.
		if entry.colorLUTDev != 0 {
			FreeLUT3D(entry.colorLUTDev)
			entry.colorLUTDev = 0
		}

		data := provider.SourceLUTData(sourceKey)
		if len(data) == 0 {
			return
		}

		// Fail closed if the declared cube dimension disagrees with the data
		// length — otherwise the kernel would index past the device buffer.
		size := provider.SourceLUTSize(sourceKey)
		if err := validateLUT3D(data, size); err != nil {
			slog.Warn("gpu: source manager: invalid color grade LUT, skipping",
				"source", sourceKey, "error", err)
			return
		}

		devPtr, err := UploadLUT3D(data)
		if err != nil {
			slog.Warn("gpu: source manager: color grade LUT upload failed",
				"source", sourceKey, "error", err)
			return
		}
		entry.colorLUTDev = devPtr
		entry.colorLUTGen = gen
		entry.colorLUTSize = size
	}

	// Apply the LUT at full intensity (per-source correction, no slider).
	// Dispatch to the correct kernel based on frame format.
	var gradeErr error
	switch frame.Format {
	case GPUFormatP210:
		gradeErr = ColorGradeLUT3DP210(m.ctx, frame, entry.colorLUTDev, entry.colorLUTSize, 1.0)
	default:
		gradeErr = ColorGradeLUT3D(m.ctx, frame, entry.colorLUTDev, entry.colorLUTSize, 1.0)
	}
	if gradeErr != nil {
		slog.Warn("gpu: source manager: color grade kernel failed",
			"source", sourceKey, "error", gradeErr, "format", frame.Format)
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

// ReadCachedFrame returns a deep copy of the latest CPU YUV420 frame for a
// source. Used by the playout live source tap to read frames without GPU
// download. Returns nil values if the source is not found or has no cached
// frame. The returned []byte is a fresh copy safe for the caller to own.
//
// The CPU cache is populated lazily: the first call for a source marks the
// cache as wanted and may return nil; subsequent ingests populate it. This
// keeps the per-ingest full-frame copy off sources nothing ever reads.
func (m *GPUSourceManager) ReadCachedFrame(sourceKey string) (yuv []byte, w, h int, pts int64) {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists || entry == nil {
		return nil, 0, 0, 0
	}

	// Mark interest so IngestYUV starts populating the cache.
	entry.cpuCacheWanted.Store(true)

	cached := entry.lastCPU.Load()
	if cached == nil {
		return nil, 0, 0, 0
	}

	// Return a deep copy to prevent data races with the next IngestYUV.
	yuvCopy := make([]byte, len(cached.yuv))
	copy(yuvCopy, cached.yuv)
	return yuvCopy, cached.w, cached.h, cached.pts
}

// GetFrame returns the latest GPU frame for a source, incrementing its
// reference count. The caller must call Release() when done. Returns nil
// if the source is not found or has no frame yet.
//
// The Load+Ref runs under the entry's curMu, shared with every publisher
// (IngestYUV/IngestGPUFrame/AliasSource/cleanup). A lock-free retry loop is
// not safe here: a publisher could Swap+Release the loaded frame to the pool
// between Load and Ref, and the Ref would resurrect a recycled frame.
func (m *GPUSourceManager) GetFrame(sourceKey string) *GPUFrame {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		return nil
	}

	return refCurrent(entry)
}

// GetFrameAt returns the source's recent frame with the given PTS, with a
// reference the caller must Release, or nil if it is no longer cached.
func (m *GPUSourceManager) GetFrameAt(sourceKey string, pts int64) *GPUFrame {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		return nil
	}
	return refAt(entry, pts)
}

// Close removes all sources and releases all GPU resources.
func (m *GPUSourceManager) Close() {
	m.mu.Lock()
	sources := m.sources
	m.sources = make(map[string]*gpuSourceEntry)
	m.mu.Unlock()

	for _, entry := range sources {
		m.cleanupEntry(entry)
	}
}

// Snapshot returns a map of source manager stats for debug/perf endpoints.
func (m *GPUSourceManager) Snapshot() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sources := make(map[string]any, len(m.sources))
	for key, entry := range m.sources {
		s := map[string]any{
			"width":          entry.width,
			"height":         entry.height,
			"has_frame":      entry.current.Load() != nil,
			"has_preview":    entry.prevEnc != nil,
			"has_stmap":      entry.stmap != nil,
			"has_colorgrade": entry.colorLUTDev != 0,
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

// AcquireNV12Tmp returns a temporary NV12-format GPU frame at the specified
// dimensions, lazily creating a per-source NV12 pool. Used by the NVDEC
// zero-copy path when the main pool is P210: NVDEC output is NV12, so we
// need an NV12 temp frame to copy into before converting to P210.
// The caller must Release() the returned frame when done.
func (m *GPUSourceManager) AcquireNV12Tmp(sourceKey string, w, h int) (*GPUFrame, error) {
	m.mu.RLock()
	entry, exists := m.sources[sourceKey]
	m.mu.RUnlock()
	if !exists {
		entry = m.autoRegister(sourceKey, w, h)
		if entry == nil {
			return nil, fmt.Errorf("gpu: AcquireNV12Tmp: auto-register failed for %s", sourceKey)
		}
	}

	// Serialize nv12Tmp pool create/close with cleanupEntry. The returned
	// frame stays valid after unlock: a closed FramePool frees frames on
	// their final Release instead of re-pooling them.
	entry.useMu.Lock()
	defer entry.useMu.Unlock()
	if entry.closed.Load() {
		return nil, fmt.Errorf("gpu: AcquireNV12Tmp: source %s was removed", sourceKey)
	}

	if entry.nv12Tmp == nil || entry.nv12TmpW != w || entry.nv12TmpH != h {
		if entry.nv12Tmp != nil {
			entry.nv12Tmp.Close()
		}
		pool, err := NewFramePool(m.ctx, w, h, 2)
		if err != nil {
			return nil, fmt.Errorf("gpu: NV12 tmp pool failed: %w", err)
		}
		entry.nv12Tmp = pool
		entry.nv12TmpW = w
		entry.nv12TmpH = h
	}

	return entry.nv12Tmp.Acquire()
}

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

	// Serialize with cleanupEntry — see IngestYUV for the rationale.
	entry.useMu.Lock()
	defer entry.useMu.Unlock()
	if entry.closed.Load() {
		frame.Release()
		return
	}

	frame.PTS = pts

	// Format conversion: if the incoming frame format doesn't match the pool
	// (e.g., NVDEC produces NV12 but pool is P210 in professional mode),
	// convert into a pool-format frame before caching.
	if frame.Format != m.pool.Format() {
		converted, err := m.pool.Acquire()
		if err != nil {
			slog.Warn("gpu: source manager: pool acquire for format convert failed",
				"source", sourceKey, "error", err)
			frame.Release()
			return
		}
		converted.PTS = frame.PTS
		if frame.Format == GPUFormatNV12 && m.pool.Format() == GPUFormatP210 {
			if err := ConvertNV12ToP210(m.ctx, converted, frame); err != nil {
				slog.Warn("gpu: source manager: NV12→P210 convert failed",
					"source", sourceKey, "error", err)
				converted.Release()
				frame.Release()
				return
			}
		} else {
			// Unsupported conversion — fall through with raw copy (may be wrong).
			if err := CopyGPUFrame(converted, frame); err != nil {
				converted.Release()
				frame.Release()
				return
			}
		}
		frame.Release()
		frame = converted
	}

	// Apply per-source ST map correction if configured.
	if m.stmaps != nil {
		m.applySourceSTMap(sourceKey, entry, frame)
	}

	// Apply per-source color grade LUT if configured.
	m.mu.RLock()
	colorGradeGPU := m.sourceColorGrade
	m.mu.RUnlock()
	if colorGradeGPU != nil {
		m.applySourceColorGrade(sourceKey, entry, frame, colorGradeGPU)
	}

	// Record dimensions before publishing — ownership of frame transfers to
	// the entry on publish, after which it may be swapped out and released.
	frameW, frameH := frame.Width, frame.Height

	// Publish: store new frame, release old.
	publishCurrent(entry, frame)

	// Per-source AI segmentation (if enabled for this source).
	m.mu.RLock()
	segEng := m.segEngine
	m.mu.RUnlock()
	if segEng != nil {
		if segEng.HasPendingConfig(sourceKey) {
			smoothing := segEng.PendingSmoothing(sourceKey)
			if err := segEng.EnableSource(sourceKey, frameW, frameH, smoothing); err != nil {
				slog.Warn("gpu: source manager: deferred segmentation enable failed",
					"source", sourceKey, "error", err)
			} else {
				segEng.ClearPendingConfig(sourceKey)
				slog.Info("gpu: source manager: deferred segmentation enabled",
					"source", sourceKey, "size", [2]int{frameW, frameH})
			}
		}

		if segEng.IsEnabled(sourceKey) {
			if gpuFrame := refCurrent(entry); gpuFrame != nil {
				segEng.Segment(sourceKey, gpuFrame)
				gpuFrame.Release()
			}
		}
	}

	// Queue frame for preview encoding.
	// nil CPU buffer — CUDA preview path copies from GPU frame (device-to-device).
	m.queuePreviewFrame(entry, nil, frameW, frameH, pts)
}

// AliasSource makes dstKey use srcKey's latest GPU frame for this tick.
// Zero-copy — swaps the atomic GPU frame pointer. The pipeline will read
// dstKey's frame from srcKey's cache. Returns true if the alias was applied
// (both sources exist and src has a frame).
//
// Reference counting: the source frame is Ref()'d before storing into dst's
// current pointer, and the old dst frame is Released. This is safe because
// the pipeline reads dstKey's current pointer via GetFrame (which also Refs),
// so the aliased frame cannot be freed while in use.
func (m *GPUSourceManager) AliasSource(dstKey, srcKey string) bool {
	m.mu.RLock()
	dstEntry, dstOK := m.sources[dstKey]
	srcEntry, srcOK := m.sources[srcKey]
	m.mu.RUnlock()

	if !dstOK || !srcOK {
		return false
	}

	if dstEntry == srcEntry {
		// Self-alias is a no-op; report whether a frame exists.
		return srcEntry.current.Load() != nil
	}

	// Take a reference to the source's current GPU frame under its curMu —
	// the Load+Ref must be atomic with respect to IngestYUV's Swap+Release
	// or we could resurrect a frame already returned to the pool.
	srcFrame := refCurrent(srcEntry)
	if srcFrame == nil {
		return false
	}
	// Read the PTS while we still hold our own reference — after the
	// publish below, ownership transfers to dst and the frame may be
	// swapped out, released, and recycled at any time.
	srcPTS := srcFrame.PTS

	// Publish into dst's current pointer; our reference transfers to dst.
	// Skip if dst was concurrently removed: cleanupEntry has already
	// drained dst.current, so publishing now would leak the reference into
	// an orphaned entry. The closed flag is set before cleanup's drain, so
	// observing it under curMu after the drain is race-free.
	dstEntry.curMu.Lock()
	if dstEntry.closed.Load() {
		dstEntry.curMu.Unlock()
		srcFrame.Release()
		return false
	}
	old := dstEntry.current.Swap(srcFrame)
	dstEntry.curMu.Unlock()
	if old != nil {
		old.Release()
	}

	// Propagate CPU-cache interest: a tap reading the alias destination
	// (ReadCachedFrame on dstKey) depends on the source entry populating
	// its CPU cache on ingest.
	if dstEntry.cpuCacheWanted.Load() {
		srcEntry.cpuCacheWanted.Store(true)
	}

	// Also alias the CPU frame cache so ReadCachedFrame(dstKey) sees it.
	srcCPU := srcEntry.lastCPU.Load()
	if srcCPU != nil {
		dstEntry.lastCPU.Store(srcCPU)
	}

	// Queue frame for preview encoding so the browser multiview stays
	// current. The preview path copies from dst's current GPU frame, so it
	// does not need the CPU cache.
	m.queuePreviewFrame(dstEntry, nil, 0, 0, srcPTS)

	return true
}

// previewLoop, queuePreviewFrame, and scaleYUV420pCPU are defined in
// source_manager_preview.go (unified GPU frame copy + ScaleBilinearOn + encode).
