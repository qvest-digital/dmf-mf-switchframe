//go:build (!cgo || !cuda) && !darwin

package gpu

// Upload returns ErrGPUNotAvailable on non-GPU builds.
func Upload(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	return ErrGPUNotAvailable
}

// Download returns ErrGPUNotAvailable on non-GPU builds.
func Download(ctx *Context, yuv []byte, frame *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// FillBlack returns ErrGPUNotAvailable on non-GPU builds.
func FillBlack(ctx *Context, frame *GPUFrame) error {
	return ErrGPUNotAvailable
}

// ScaleQuality selects the scaling algorithm.
type ScaleQuality int

const (
	ScaleQualityBilinear ScaleQuality = iota
	ScaleQualityLanczos
)

// ScaleBilinear returns ErrGPUNotAvailable on non-GPU builds.
func ScaleBilinear(ctx *Context, dst, src *GPUFrame) error { return ErrGPUNotAvailable }

// ScaleBilinearOn returns ErrGPUNotAvailable on non-GPU builds.
func ScaleBilinearOn(ctx *Context, dst, src *GPUFrame, q *GPUWorkQueue) error {
	return ErrGPUNotAvailable
}

// ScaleLanczos3 returns ErrGPUNotAvailable on non-GPU builds.
func ScaleLanczos3(ctx *Context, dst, src *GPUFrame) error { return ErrGPUNotAvailable }

// Scale returns ErrGPUNotAvailable on non-GPU builds.
func Scale(ctx *Context, dst, src *GPUFrame, quality ScaleQuality) error { return ErrGPUNotAvailable }

// WipeDirection matches transition.WipeDirection values.
type WipeDirection int

// BlendMix returns ErrGPUNotAvailable on non-GPU builds.
func BlendMix(ctx *Context, dst, a, b *GPUFrame, position float64) error { return ErrGPUNotAvailable }

// BlendFTB returns ErrGPUNotAvailable on non-GPU builds.
func BlendFTB(ctx *Context, dst, src *GPUFrame, position float64) error { return ErrGPUNotAvailable }

// BlendWipe returns ErrGPUNotAvailable on non-GPU builds.
func BlendWipe(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	return ErrGPUNotAvailable
}

// BlendStinger returns ErrGPUNotAvailable on non-GPU builds.
func BlendStinger(ctx *Context, dst, base, overlay, alpha *GPUFrame) error { return ErrGPUNotAvailable }

// ChromaKeyConfig holds chroma key parameters.
type ChromaKeyConfig struct {
	KeyCb, KeyCr   uint8
	Similarity     float32
	Smoothness     float32
	SpillSuppress  float32
	SpillReplaceCb uint8
	SpillReplaceCr uint8
}

// ChromaKey returns ErrGPUNotAvailable on non-GPU builds.
func ChromaKey(ctx *Context, frame *GPUFrame, maskBuf *GPUFrame, cfg ChromaKeyConfig) error {
	return ErrGPUNotAvailable
}

// LumaKey returns ErrGPUNotAvailable on non-GPU builds.
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

// PIPComposite returns ErrGPUNotAvailable on non-GPU builds.
func PIPComposite(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return ErrGPUNotAvailable
}

// PIPCompositeWithCrop returns ErrGPUNotAvailable on non-GPU builds.
func PIPCompositeWithCrop(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
	cropX, cropY, cropW, cropH int) error {
	return ErrGPUNotAvailable
}

// DrawBorder returns ErrGPUNotAvailable on non-GPU builds.
func DrawBorder(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	return ErrGPUNotAvailable
}

// FillRect returns ErrGPUNotAvailable on non-GPU builds.
func FillRect(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	return ErrGPUNotAvailable
}

// GPUOverlay is a stub for non-GPU builds.
type GPUOverlay struct {
	Width, Height int
}

// UploadOverlay returns ErrGPUNotAvailable on non-GPU builds.
func UploadOverlay(ctx *Context, rgba []byte, width, height int) (*GPUOverlay, error) {
	return nil, ErrGPUNotAvailable
}

// FreeOverlay is a no-op on non-GPU builds.
func FreeOverlay(overlay *GPUOverlay) {}

// DSKCompositeFullFrame returns ErrGPUNotAvailable on non-GPU builds.
func DSKCompositeFullFrame(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
	return ErrGPUNotAvailable
}

// DSKCompositeRect returns ErrGPUNotAvailable on non-GPU builds.
func DSKCompositeRect(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	return ErrGPUNotAvailable
}

// GPUSTMap is a stub for non-GPU builds.
type GPUSTMap struct{ Width, Height int }

// UploadSTMap returns ErrGPUNotAvailable on non-GPU builds.
func UploadSTMap(ctx *Context, s, t []float32, width, height int) (*GPUSTMap, error) {
	return nil, ErrGPUNotAvailable
}

// Free is a no-op on non-GPU builds.
func (m *GPUSTMap) Free() {}

// STMapWarp returns ErrGPUNotAvailable on non-GPU builds.
func STMapWarp(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	return ErrGPUNotAvailable
}

// STMapWarpGlobalMem returns ErrGPUNotAvailable on non-GPU builds.
func STMapWarpGlobalMem(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	return ErrGPUNotAvailable
}

// GPUAnimatedSTMap is a stub for non-GPU builds.
type GPUAnimatedSTMap struct{ Width, Height, FPS int }

// NewGPUAnimatedSTMap returns ErrGPUNotAvailable on non-GPU builds.
func NewGPUAnimatedSTMap(ctx *Context, sMaps, tMaps [][]float32, width, height, fps int) (*GPUAnimatedSTMap, error) {
	return nil, ErrGPUNotAvailable
}

// CurrentFrame returns nil on non-GPU builds.
func (a *GPUAnimatedSTMap) CurrentFrame() *GPUSTMap { return nil }

// FrameCount returns 0 on non-GPU builds.
func (a *GPUAnimatedSTMap) FrameCount() int { return 0 }

// Free is a no-op on non-GPU builds.
func (a *GPUAnimatedSTMap) Free() {}
