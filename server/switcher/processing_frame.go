package switcher

import "sync"

// yuvPool recycles YUV420 frame buffers to avoid 3MB/frame allocations
// on every pipeline decode. Seeded with 1080p buffers; getYUVBuffer
// transparently allocates larger buffers for higher resolutions.
var yuvPool = sync.Pool{
	New: func() any {
		// Start with 1080p size; will grow as needed
		return make([]byte, 1920*1080*3/2)
	},
}

// getYUVBuffer retrieves a YUV buffer from the pool, growing if needed.
func getYUVBuffer(size int) []byte {
	buf := yuvPool.Get().([]byte)
	if cap(buf) < size {
		buf = make([]byte, size)
	}
	return buf[:size]
}

// putYUVBuffer returns a YUV buffer to the pool for reuse.
func putYUVBuffer(buf []byte) {
	if buf != nil {
		yuvPool.Put(buf) //nolint:staticcheck // slice value is intentional
	}
}

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
}

// ReleaseYUV returns the YUV buffer to the pool. Call after encode has
// finished using the buffer. Safe to call multiple times or on nil YUV.
func (pf *ProcessingFrame) ReleaseYUV() {
	if pf.YUV != nil {
		putYUVBuffer(pf.YUV)
		pf.YUV = nil
	}
}

// DeepCopy returns a new ProcessingFrame with a copied YUV buffer.
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
	cp := *pf
	cp.YUV = getYUVBuffer(len(pf.YUV))
	copy(cp.YUV, pf.YUV)
	return &cp
}
