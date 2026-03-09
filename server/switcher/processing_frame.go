package switcher

// ProcessingFrame carries decoded YUV420 data through the video processing chain.
// Created by decoding a media.VideoFrame, consumed by encoding back to one.
// Used only inside the switcher pipeline — not a replacement for media.VideoFrame.
type ProcessingFrame struct {
	YUV        []byte // YUV420 planar: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
	Width      int
	Height     int
	PTS        int64
	DTS        int64
	IsKeyframe bool
	GroupID    uint32
	Codec      string // preserved from source for output metadata

	// pool is the FramePool this buffer was acquired from.
	// nil-safe: falls back to make()/no-op for tests and transient wrappers.
	pool *FramePool
}

// ReleaseYUV returns the YUV buffer to the pool. Call after encode has
// finished using the buffer. Safe to call multiple times or on nil YUV.
func (pf *ProcessingFrame) ReleaseYUV() {
	if pf.YUV != nil {
		if pf.pool != nil {
			pf.pool.Release(pf.YUV)
		}
		pf.YUV = nil
	}
}

// DeepCopy returns a new ProcessingFrame with a copied YUV buffer.
// Inherits the pool reference so the copy's buffer can be released.
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
	cp := *pf
	if pf.pool != nil {
		cp.YUV = pf.pool.Acquire()
	} else {
		cp.YUV = make([]byte, len(pf.YUV))
	}
	copy(cp.YUV, pf.YUV)
	return &cp
}
