package switcher

import (
	"log/slog"
	"sync/atomic"
)

// GPURefCounter is an interface for GPU frame reference counting.
// When GPUData implements this interface, DeepCopy will call Ref()
// and ReleaseYUV will call Release() to maintain correct GPU memory lifecycle.
type GPURefCounter interface {
	Ref()
	Release()
}

// ProcessingFrame carries decoded pixel data through the video processing chain.
// Created by decoding a media.VideoFrame, consumed by encoding back to one.
// Used only inside the switcher pipeline — not a replacement for media.VideoFrame.
//
// Reference counting: frames that flow through the pipeline should be created
// with refs=1 (via SetRefs). Pipeline nodes that share the frame with sinks
// call Ref() before and ReleaseYUV() after. The buffer returns to the pool
// only when the last reference is dropped. Unmanaged frames (refs==nil, the
// zero value) release immediately on ReleaseYUV — this preserves backward
// compatibility with test code and transient frames.
//
// The refs pointer is shared across value copies of a ProcessingFrame, so
// frame_sync's pattern of `releaseRawVideo = *newest` correctly shares the
// refcount. This follows the FFmpeg AVBufferRef model.
type ProcessingFrame struct {
	Data       []byte              // Raw planar pixel data (layout depends on pipeline color format)
	Format     PipelineColorFormat // Color format of this frame's data
	Width      int
	Height     int
	PTS        int64
	DTS        int64
	IsKeyframe bool
	GroupID    uint32
	Codec      string // preserved from source for output metadata

	// ArrivalNano records UnixNano when the frame entered sourceViewer.SendVideo().
	// Used for E2E latency measurement (source arrival → pipeline processing complete).
	ArrivalNano int64

	// DecodeStartNano records UnixNano when decodeLoop dequeues this frame from
	// the sourceDecoder channel (T1 in the latency breakdown).
	DecodeStartNano int64

	// DecodeEndNano records UnixNano after the H.264 decode completes for this
	// frame (T2 in the latency breakdown).
	DecodeEndNano int64

	// SyncReleaseNano records UnixNano when frame_sync releases this frame in
	// releaseTick Phase 3 (T3 in the latency breakdown).
	SyncReleaseNano int64

	// GPUData holds a reference to the GPU-resident frame (e.g., *gpu.GPUFrame)
	// when the frame is being processed on the GPU. Set by the GPU upload node,
	// cleared by the GPU download node. Typed as `any` to avoid a circular
	// import between switcher and gpu packages. GPU pipeline nodes type-assert
	// this to access the underlying GPU frame.
	GPUData any

	// pool is the FramePool this buffer was acquired from.
	// nil-safe: falls back to make()/no-op for tests and transient wrappers.
	pool *FramePool

	// refs is a shared pointer to the atomic reference count. nil means
	// unmanaged — ReleaseYUV releases immediately. When non-nil, the buffer
	// returns to the pool only when the count decrements to 0. The pointer
	// is shared across Go value copies so frame_sync delivery copies and
	// pipeline shallow copies all decrement the same counter.
	refs *atomic.Int32
}

// SetRefs allocates (if needed) and initializes the reference count. Call
// once after creation, before the frame is shared with any other goroutine.
// Typically set to 1 for frames entering the pipeline.
func (pf *ProcessingFrame) SetRefs(n int32) {
	if pf.refs == nil {
		pf.refs = &atomic.Int32{}
	}
	pf.refs.Store(n)
}

// Refs returns the current reference count (for diagnostics/testing).
// Returns 0 for unmanaged frames (nil refs).
func (pf *ProcessingFrame) Refs() int32 {
	if pf.refs == nil {
		return 0
	}
	return pf.refs.Load()
}

// Ref increments the reference count, indicating an additional consumer
// holds a reference to this frame's data buffer. No-op on unmanaged frames.
func (pf *ProcessingFrame) Ref() {
	if pf.refs == nil {
		return
	}
	pf.refs.Add(1)
}

// ReleaseYUV returns the data buffer to the pool when the last reference is
// dropped. For refcounted frames (refs >= 1), decrements and releases only
// when refs reaches 0. For unmanaged frames (nil refs), releases immediately.
// Safe to call multiple times; subsequent calls on nil Data are no-ops.
//
// Also releases GPUData if it implements GPURefCounter (decrements GPU
// frame reference count, freeing GPU memory when the last reference drops).
func (pf *ProcessingFrame) ReleaseYUV() {
	if pf.Data == nil {
		return
	}
	if pf.refs != nil {
		new := pf.refs.Add(-1)
		if new > 0 {
			return
		}
		if new < 0 {
			// Log and leak the buffer rather than panicking — a leaked buffer
			// is invisible to viewers, but a panic takes down the broadcast.
			slog.Error("ProcessingFrame.ReleaseYUV: refcount underflow (double release)",
				"refs", new)
			return
		}
	}
	// Release GPU data only when the last reference drops, so concurrent
	// consumers (e.g., async encode goroutine) aren't racing on GPUData.
	if rc, ok := pf.GPUData.(GPURefCounter); ok {
		rc.Release()
		pf.GPUData = nil
	}
	if pf.pool != nil {
		pf.pool.Release(pf.Data)
	}
	pf.Data = nil
}

// MakeWritable ensures this frame has exclusive ownership of its data buffer.
// If the refcount is > 1 (buffer shared with frame_sync, another consumer, etc.),
// acquires a new buffer from the pool, copies the data, and detaches from the
// shared refcount. If the frame is already the sole owner (refs <= 1) or
// unmanaged (nil refs), this is a no-op.
//
// Follows the FFmpeg av_frame_make_writable() / GStreamer gst_buffer_make_writable()
// pattern. The pipeline calls this at entry so compositor nodes can safely modify
// data in-place without aliasing source frames retained by frame_sync.
func (pf *ProcessingFrame) MakeWritable(pool *FramePool) {
	if pf.Data == nil {
		return
	}
	if pf.refs == nil || pf.refs.Load() <= 1 {
		return // already sole owner or unmanaged
	}

	// Shared buffer — acquire our own copy.
	var newBuf []byte
	if pool != nil && len(pf.Data) <= pool.bufSize {
		newBuf = pool.Acquire()[:len(pf.Data)]
	} else {
		newBuf = make([]byte, len(pf.Data))
	}
	copy(newBuf, pf.Data)

	// Release our ref on the shared buffer. Normally the other owner
	// (lastRawVideo) still holds a ref so this stays > 0. In the rare
	// case where the frame sync tick raced and already released its ref,
	// we drop to 0 and must return the old buffer to the pool ourselves.
	oldData := pf.Data
	oldPool := pf.pool
	if pf.refs.Add(-1) <= 0 {
		if oldPool != nil {
			oldPool.Release(oldData)
		}
	}

	// Detach: independent refcount, own buffer, own pool reference.
	pf.refs = &atomic.Int32{}
	pf.refs.Store(1)
	pf.Data = newBuf
	pf.pool = pool
}

// SetPool sets the FramePool that owns this frame's data buffer.
// ReleaseYUV will return the buffer to the pool when all references
// are dropped. Exported so external packages (e.g., SRT wiring in
// main) can set pool ownership on frames they create.
func (pf *ProcessingFrame) SetPool(pool *FramePool) {
	pf.pool = pool
}

// DeepCopy returns a new ProcessingFrame with a copied data buffer.
// The copy starts with nil refs (unmanaged, independent lifecycle).
// Caller should call SetRefs(1) if the copy will flow through the pipeline.
//
// If GPUData implements GPURefCounter, Ref() is called so the GPU frame
// stays alive as long as the copy exists. The caller must ensure
// ReleaseYUV is called on the copy to release the GPU reference.
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
	cp := *pf
	cp.refs = nil // independent lifecycle — new allocation on SetRefs
	if pf.pool != nil && len(pf.Data) <= pf.pool.bufSize {
		cp.Data = pf.pool.Acquire()[:len(pf.Data)]
	} else {
		cp.Data = make([]byte, len(pf.Data))
	}
	copy(cp.Data, pf.Data)

	// Increment GPU frame reference count if present
	if rc, ok := cp.GPUData.(GPURefCounter); ok {
		rc.Ref()
	}

	return &cp
}

// YPlane returns the Y (luma) plane slice.
func (pf *ProcessingFrame) YPlane() []byte {
	size := pf.Format.YPlaneSize(pf.Width, pf.Height)
	if len(pf.Data) < size {
		return pf.Data
	}
	return pf.Data[:size]
}

// CbPlane returns the Cb (blue-difference chroma) plane slice.
func (pf *ProcessingFrame) CbPlane() []byte {
	ySize := pf.Format.YPlaneSize(pf.Width, pf.Height)
	cSize := pf.Format.ChromaPlaneSize(pf.Width, pf.Height)
	end := ySize + cSize
	if len(pf.Data) < end {
		return nil
	}
	return pf.Data[ySize:end]
}

// CrPlane returns the Cr (red-difference chroma) plane slice.
func (pf *ProcessingFrame) CrPlane() []byte {
	ySize := pf.Format.YPlaneSize(pf.Width, pf.Height)
	cSize := pf.Format.ChromaPlaneSize(pf.Width, pf.Height)
	start := ySize + cSize
	end := ySize + 2*cSize
	if len(pf.Data) < end {
		return nil
	}
	return pf.Data[start:end]
}

// FrameDataSize returns the expected total byte count for this frame's format and dimensions.
func (pf *ProcessingFrame) FrameDataSize() int {
	return pf.Format.FrameSize(pf.Width, pf.Height)
}
