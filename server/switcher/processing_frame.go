package switcher

import (
	"log/slog"
	"sync/atomic"
)

// ProcessingFrame carries decoded YUV420 data through the video processing chain.
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
	YUV        []byte // YUV420 planar: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
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
// holds a reference to this frame's YUV buffer. No-op on unmanaged frames.
func (pf *ProcessingFrame) Ref() {
	if pf.refs == nil {
		return
	}
	pf.refs.Add(1)
}

// ReleaseYUV returns the YUV buffer to the pool when the last reference is
// dropped. For refcounted frames (refs ≥ 1), decrements and releases only
// when refs reaches 0. For unmanaged frames (nil refs), releases immediately.
// Safe to call multiple times; subsequent calls on nil YUV are no-ops.
func (pf *ProcessingFrame) ReleaseYUV() {
	if pf.YUV == nil {
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
	if pf.pool != nil {
		pf.pool.Release(pf.YUV)
	}
	pf.YUV = nil
}

// MakeWritable ensures this frame has exclusive ownership of its YUV buffer.
// If the refcount is > 1 (buffer shared with frame_sync, another consumer, etc.),
// acquires a new buffer from the pool, copies the data, and detaches from the
// shared refcount. If the frame is already the sole owner (refs ≤ 1) or
// unmanaged (nil refs), this is a no-op.
//
// Follows the FFmpeg av_frame_make_writable() / GStreamer gst_buffer_make_writable()
// pattern. The pipeline calls this at entry so compositor nodes can safely modify
// YUV in-place without aliasing source frames retained by frame_sync.
func (pf *ProcessingFrame) MakeWritable(pool *FramePool) {
	if pf.YUV == nil {
		return
	}
	if pf.refs == nil || pf.refs.Load() <= 1 {
		return // already sole owner or unmanaged
	}

	// Shared buffer — acquire our own copy.
	var newYUV []byte
	if pool != nil && len(pf.YUV) <= pool.bufSize {
		newYUV = pool.Acquire()[:len(pf.YUV)]
	} else {
		newYUV = make([]byte, len(pf.YUV))
	}
	copy(newYUV, pf.YUV)

	// Release our ref on the shared buffer. If this drops it to 0 the
	// original owner's ReleaseYUV will be a no-op (already returned).
	pf.refs.Add(-1)

	// Detach: independent refcount, own buffer, own pool reference.
	pf.refs = &atomic.Int32{}
	pf.refs.Store(1)
	pf.YUV = newYUV
	pf.pool = pool
}

// DeepCopy returns a new ProcessingFrame with a copied YUV buffer.
// The copy starts with nil refs (unmanaged, independent lifecycle).
// Caller should call SetRefs(1) if the copy will flow through the pipeline.
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
	cp := *pf
	cp.refs = nil // independent lifecycle — new allocation on SetRefs
	if pf.pool != nil {
		cp.YUV = pf.pool.Acquire()[:len(pf.YUV)]
	} else {
		cp.YUV = make([]byte, len(pf.YUV))
	}
	copy(cp.YUV, pf.YUV)
	return &cp
}
