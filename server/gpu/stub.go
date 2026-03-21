//go:build !cgo || !cuda

package gpu

// Context is a stub for non-CUDA builds.
type Context struct{}

// NewContext returns ErrGPUNotAvailable on non-CUDA builds.
func NewContext() (*Context, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-CUDA builds.
func (c *Context) Close() error { return nil }

// DeviceProperties returns a zero-value DeviceProperties.
func (c *Context) DeviceProperties() DeviceProperties { return DeviceProperties{} }

// Stream returns 0 (nil stream).
func (c *Context) Stream() uintptr { return 0 }

// EncStream returns 0 (nil stream).
func (c *Context) EncStream() uintptr { return 0 }

// MemoryStats returns zero stats on non-CUDA builds.
func (c *Context) MemoryStats() MemoryStats { return MemoryStats{} }

// Sync is a no-op on non-CUDA builds.
func (c *Context) Sync() error { return nil }

// SetPool is a no-op on non-CUDA builds.
func (c *Context) SetPool(pool *FramePool) {}

// Pool returns nil on non-CUDA builds.
func (c *Context) Pool() *FramePool { return nil }

// DeviceProperties holds GPU device information.
type DeviceProperties struct {
	Name               string
	ComputeCapability  [2]int
	TotalMemory        int64
	MultiprocessorCount int
	MaxThreadsPerBlock int
}

// MemoryStats holds GPU memory usage information.
type MemoryStats struct {
	TotalMB int
	FreeMB  int
	UsedMB  int
}

// GPUFrame is a stub for non-CUDA builds.
type GPUFrame struct {
	Width  int
	Height int
	Pitch  int
	PTS    int64
}

// Release is a no-op on non-CUDA builds.
func (f *GPUFrame) Release() {}

// Ref is a no-op on non-CUDA builds.
func (f *GPUFrame) Ref() {}

// FramePool is a stub for non-CUDA builds.
type FramePool struct{}

// NewFramePool returns ErrGPUNotAvailable on non-CUDA builds.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	return nil, ErrGPUNotAvailable
}

// Acquire returns ErrGPUNotAvailable on non-CUDA builds.
func (p *FramePool) Acquire() (*GPUFrame, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-CUDA builds.
func (p *FramePool) Close() {}

// Stats returns zero stats on non-CUDA builds.
func (p *FramePool) Stats() (hits, misses uint64) { return 0, 0 }

// Upload returns ErrGPUNotAvailable on non-CUDA builds.
func Upload(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	return ErrGPUNotAvailable
}

// Download returns ErrGPUNotAvailable on non-CUDA builds.
func Download(ctx *Context, yuv []byte, frame *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// FillBlack returns ErrGPUNotAvailable on non-CUDA builds.
func FillBlack(ctx *Context, frame *GPUFrame) error {
	return ErrGPUNotAvailable
}

// ScaleQuality selects the scaling algorithm.
type ScaleQuality int

const (
	ScaleQualityBilinear ScaleQuality = iota
	ScaleQualityLanczos
)

// ScaleBilinear returns ErrGPUNotAvailable on non-CUDA builds.
func ScaleBilinear(ctx *Context, dst, src *GPUFrame) error { return ErrGPUNotAvailable }

// Scale returns ErrGPUNotAvailable on non-CUDA builds.
func Scale(ctx *Context, dst, src *GPUFrame, quality ScaleQuality) error { return ErrGPUNotAvailable }

// WipeDirection matches transition.WipeDirection values.
type WipeDirection int

// BlendMix returns ErrGPUNotAvailable on non-CUDA builds.
func BlendMix(ctx *Context, dst, a, b *GPUFrame, position float64) error { return ErrGPUNotAvailable }

// BlendFTB returns ErrGPUNotAvailable on non-CUDA builds.
func BlendFTB(ctx *Context, dst, src *GPUFrame, position float64) error { return ErrGPUNotAvailable }

// BlendWipe returns ErrGPUNotAvailable on non-CUDA builds.
func BlendWipe(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	return ErrGPUNotAvailable
}

// BlendStinger returns ErrGPUNotAvailable on non-CUDA builds.
func BlendStinger(ctx *Context, dst, base, overlay, alpha *GPUFrame) error { return ErrGPUNotAvailable }

// ChromaKeyConfig holds chroma key parameters.
type ChromaKeyConfig struct {
	KeyCb, KeyCr       uint8
	Similarity         float32
	Smoothness         float32
	SpillSuppress      float32
	SpillReplaceCb     uint8
	SpillReplaceCr     uint8
}

// ChromaKey returns ErrGPUNotAvailable on non-CUDA builds.
func ChromaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	return ErrGPUNotAvailable
}

// LumaKey returns ErrGPUNotAvailable on non-CUDA builds.
func LumaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, lut [256]byte) error {
	return ErrGPUNotAvailable
}

// BuildLumaKeyLUT creates a 256-byte lookup table for luma keying.
func BuildLumaKeyLUT(lowClip, highClip, softness float32) [256]byte { return [256]byte{} }

// Rect defines a rectangle for compositing.
type Rect struct{ X, Y, W, H int }

// YUVColor defines a color in YCbCr space.
type YUVColor struct{ Y, Cb, Cr uint8 }

// ColorBlack is BT.709 limited-range black.
var ColorBlack = YUVColor{16, 128, 128}

// PIPComposite returns ErrGPUNotAvailable on non-CUDA builds.
func PIPComposite(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return ErrGPUNotAvailable
}

// DrawBorder returns ErrGPUNotAvailable on non-CUDA builds.
func DrawBorder(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	return ErrGPUNotAvailable
}

// FillRect returns ErrGPUNotAvailable on non-CUDA builds.
func FillRect(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	return ErrGPUNotAvailable
}

// GPUOverlay is a stub for non-CUDA builds.
type GPUOverlay struct {
	Width, Height int
}

// UploadOverlay returns ErrGPUNotAvailable on non-CUDA builds.
func UploadOverlay(ctx *Context, rgba []byte, width, height int) (*GPUOverlay, error) {
	return nil, ErrGPUNotAvailable
}

// FreeOverlay is a no-op on non-CUDA builds.
func FreeOverlay(overlay *GPUOverlay) {}

// DSKCompositeFullFrame returns ErrGPUNotAvailable on non-CUDA builds.
func DSKCompositeFullFrame(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
	return ErrGPUNotAvailable
}

// DSKCompositeRect returns ErrGPUNotAvailable on non-CUDA builds.
func DSKCompositeRect(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	return ErrGPUNotAvailable
}

// GPUSTMap is a stub for non-CUDA builds.
type GPUSTMap struct{ Width, Height int }

// UploadSTMap returns ErrGPUNotAvailable on non-CUDA builds.
func UploadSTMap(ctx *Context, s, t []float32, width, height int) (*GPUSTMap, error) {
	return nil, ErrGPUNotAvailable
}

// Free is a no-op on non-CUDA builds.
func (m *GPUSTMap) Free() {}

// STMapWarp returns ErrGPUNotAvailable on non-CUDA builds.
func STMapWarp(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	return ErrGPUNotAvailable
}

// GPUAnimatedSTMap is a stub for non-CUDA builds.
type GPUAnimatedSTMap struct{ Width, Height, FPS int }

// NewGPUAnimatedSTMap returns ErrGPUNotAvailable on non-CUDA builds.
func NewGPUAnimatedSTMap(ctx *Context, sMaps, tMaps [][]float32, width, height, fps int) (*GPUAnimatedSTMap, error) {
	return nil, ErrGPUNotAvailable
}

// CurrentFrame returns nil on non-CUDA builds.
func (a *GPUAnimatedSTMap) CurrentFrame() *GPUSTMap { return nil }

// FrameCount returns 0 on non-CUDA builds.
func (a *GPUAnimatedSTMap) FrameCount() int { return 0 }

// Free is a no-op on non-CUDA builds.
func (a *GPUAnimatedSTMap) Free() {}

// FRUC is a stub for non-CUDA builds.
type FRUC struct{}

// FRUCAvailable returns false on non-CUDA builds.
func FRUCAvailable() bool { return false }

// NewFRUC returns ErrGPUNotAvailable on non-CUDA builds.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) { return nil, ErrGPUNotAvailable }

// Interpolate returns ErrGPUNotAvailable on non-CUDA builds.
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame, alpha float64) error {
	return ErrGPUNotAvailable
}

// Close is a no-op on non-CUDA builds.
func (f *FRUC) Close() {}

// GPUEncoder is a stub for non-CUDA builds.
type GPUEncoder struct{}

// NewGPUEncoder returns ErrGPUNotAvailable on non-CUDA builds.
func NewGPUEncoder(ctx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// EncodeGPU returns ErrGPUNotAvailable on non-CUDA builds.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// EncodeCPU returns ErrGPUNotAvailable on non-CUDA builds.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// Close is a no-op on non-CUDA builds.
func (e *GPUEncoder) Close() {}

// GPUDecoder is a stub for non-CUDA builds.
type GPUDecoder struct{}

// NewGPUDecoder returns ErrGPUNotAvailable on non-CUDA builds.
func NewGPUDecoder(ctx *Context, threadCount int) (*GPUDecoder, error) {
	return nil, ErrGPUNotAvailable
}
