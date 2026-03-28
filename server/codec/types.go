package codec

// yuvFrameSize returns the byte size of a YUV420 8-bit frame (w*h*3/2).
// Local helper to avoid importing the switcher package.
func yuvFrameSize(w, h int) int {
	return w * h * 3 / 2
}

// EncoderInfo describes an available video encoder backend.
type EncoderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
}
