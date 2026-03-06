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
}

// DeepCopy returns a new ProcessingFrame with a copied YUV buffer.
func (pf *ProcessingFrame) DeepCopy() *ProcessingFrame {
	cp := *pf
	cp.YUV = make([]byte, len(pf.YUV))
	copy(cp.YUV, pf.YUV)
	return &cp
}
