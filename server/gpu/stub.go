//go:build (!cgo || !cuda) && !darwin

package gpu

import "time"

// Ensure time is used (referenced by GPUPipelineNode interface).
var _ = time.Duration(0)

// Context is a stub for non-GPU builds.
type Context struct{}

// NewContext returns ErrGPUNotAvailable on non-GPU builds.
func NewContext() (*Context, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-GPU builds.
func (c *Context) Close() error { return nil }

// Backend returns "" on non-GPU builds.
func (c *Context) Backend() string { return "" }

// DeviceName returns "" on non-GPU builds.
func (c *Context) DeviceName() string { return "" }

// DeviceProperties returns a zero-value DeviceProperties.
func (c *Context) DeviceProperties() DeviceProperties { return DeviceProperties{} }

// Stream returns 0 (nil stream).
func (c *Context) Stream() uintptr { return 0 }

// EncStream returns 0 (nil stream).
func (c *Context) EncStream() uintptr { return 0 }

// MemoryStats returns zero stats on non-GPU builds.
func (c *Context) MemoryStats() MemoryStats { return MemoryStats{} }

// Sync is a no-op on non-GPU builds.
func (c *Context) Sync() error { return nil }

// SetPool is a no-op on non-GPU builds.
func (c *Context) SetPool(pool *FramePool) {}

// Pool returns nil on non-GPU builds.
func (c *Context) Pool() *FramePool { return nil }

// DeviceProperties holds GPU device information.
type DeviceProperties struct {
	Name                string
	ComputeCapability   [2]int
	TotalMemory         int64
	MultiprocessorCount int
	MaxThreadsPerBlock  int
}

// MemoryStats holds GPU memory usage information.
type MemoryStats struct {
	TotalMB int
	FreeMB  int
	UsedMB  int
}

// GPUFrame is a stub for non-GPU builds.
type GPUFrame struct {
	Width  int
	Height int
	Pitch  int
	PTS    int64
}

// Release is a no-op on non-GPU builds.
func (f *GPUFrame) Release() {}

// Ref is a no-op on non-GPU builds.
func (f *GPUFrame) Ref() {}

// FramePool is a stub for non-GPU builds.
type FramePool struct{}

// NewFramePool returns ErrGPUNotAvailable on non-GPU builds.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	return nil, ErrGPUNotAvailable
}

// Acquire returns ErrGPUNotAvailable on non-GPU builds.
func (p *FramePool) Acquire() (*GPUFrame, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-GPU builds.
func (p *FramePool) Close() {}

// Stats returns zero stats on non-GPU builds.
func (p *FramePool) Stats() (hits, misses uint64) { return 0, 0 }

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

// FRUC is a stub for non-GPU builds.
type FRUC struct{}

// FRUCAvailable returns false on non-GPU builds.
func FRUCAvailable() bool { return false }

// NewFRUC returns ErrGPUNotAvailable on non-GPU builds.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) { return nil, ErrGPUNotAvailable }

// Interpolate returns ErrGPUNotAvailable on non-GPU builds.
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame, alpha float64) error {
	return ErrGPUNotAvailable
}

// Close is a no-op on non-GPU builds.
func (f *FRUC) Close() {}

// Timer is a stub for non-GPU builds.
type Timer struct{}

// NewTimer returns ErrGPUNotAvailable on non-GPU builds.
func NewTimer() (*Timer, error) { return nil, ErrGPUNotAvailable }

// Start is a no-op on non-GPU builds.
func (t *Timer) Start(stream uintptr) {}

// Stop returns 0 on non-GPU builds.
func (t *Timer) Stop(stream uintptr) float32 { return 0 }

// Close is a no-op on non-GPU builds.
func (t *Timer) Close() {}

// PipelineMetrics tracks GPU pipeline performance counters.
type PipelineMetrics struct{}

// Snapshot returns empty stats on non-GPU builds.
func (m *PipelineMetrics) Snapshot() map[string]any { return map[string]any{} }

// MemoryStatsExtended returns unavailable status on non-GPU builds.
func MemoryStatsExtended(ctx *Context) map[string]any {
	return map[string]any{"available": false}
}

// GPUPipelineNode is the interface for GPU pipeline processing nodes.
type GPUPipelineNode interface {
	Name() string
	Configure(width, height, pitch int) error
	Active() bool
	ProcessGPU(frame *GPUFrame) error
	Err() error
	Latency() time.Duration
	Close() error
}

// GPUPipeline is a stub for non-GPU builds.
type GPUPipeline struct{}

// NewGPUPipeline returns a stub on non-GPU builds.
func NewGPUPipeline(ctx *Context, pool *FramePool) *GPUPipeline { return &GPUPipeline{} }

// Pool returns nil on non-GPU builds.
func (p *GPUPipeline) Pool() *FramePool { return nil }

// Build returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) Build(width, height, pitch int, nodes []GPUPipelineNode) error {
	return ErrGPUNotAvailable
}

// Run returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) Run(frame *GPUFrame) error { return ErrGPUNotAvailable }

// RunWithUpload returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) RunWithUpload(yuv []byte, width, height int, pts int64) (*GPUFrame, error) {
	return nil, ErrGPUNotAvailable
}

// Snapshot returns empty stats on non-GPU builds.
func (p *GPUPipeline) Snapshot() map[string]any { return map[string]any{"gpu": false} }

// Wait is a no-op on non-GPU builds.
func (p *GPUPipeline) Wait() {}

// Close is a no-op on non-GPU builds.
func (p *GPUPipeline) Close() error { return nil }

// RawSinkFunc is a callback for raw YUV420p frames.
type RawSinkFunc func(yuv []byte, width, height int)

// V210LineStride returns the byte stride for one line of V210 data.
func V210LineStride(width int) int {
	groups := (width + 5) / 6
	rawBytes := groups * 16
	return (rawBytes + 127) &^ 127
}

// V210ToNV12 returns ErrGPUNotAvailable on non-GPU builds.
func V210ToNV12(ctx *Context, dst *GPUFrame, v210DevPtr uintptr, v210Stride, width, height int) error {
	return ErrGPUNotAvailable
}

// NV12ToV210 returns ErrGPUNotAvailable on non-GPU builds.
func NV12ToV210(ctx *Context, v210DevPtr uintptr, v210Stride int, src *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// UploadV210 returns ErrGPUNotAvailable on non-GPU builds.
func UploadV210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	return ErrGPUNotAvailable
}

// DownloadV210 returns ErrGPUNotAvailable on non-GPU builds.
func DownloadV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// PreviewEncoder is a stub for non-GPU builds.
type PreviewEncoder struct{}

// NewPreviewEncoder returns ErrGPUNotAvailable on non-GPU builds.
func NewPreviewEncoder(ctx *Context, srcW, srcH, dstW, dstH, bitrate, fpsNum, fpsDen int) (*PreviewEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// Encode returns ErrGPUNotAvailable on non-GPU builds.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// Close is a no-op on non-GPU builds.
func (p *PreviewEncoder) Close() {}

// GPUEncoder is a stub for non-GPU builds.
type GPUEncoder struct{}

// NewGPUEncoder returns ErrGPUNotAvailable on non-GPU builds.
func NewGPUEncoder(ctx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// EncodeGPU returns ErrGPUNotAvailable on non-GPU builds.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// EncodeCPU returns ErrGPUNotAvailable on non-GPU builds.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// IsNativeVT returns false on non-GPU builds.
func (e *GPUEncoder) IsNativeVT() bool { return false }

// Close is a no-op on non-GPU builds.
func (e *GPUEncoder) Close() {}

// GPUDecoder is a stub for non-GPU builds.
type GPUDecoder struct{}

// NewGPUDecoder returns ErrGPUNotAvailable on non-GPU builds.
func NewGPUDecoder(ctx *Context, threadCount int) (*GPUDecoder, error) {
	return nil, ErrGPUNotAvailable
}
