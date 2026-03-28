//go:build cgo && cuda && tensorrt

package arinfer

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

/*
#include <cuda_runtime.h>
#include <stdint.h>

// nv12_to_rgb_chw is the fused NV12 -> RGB float32 CHW preprocessing kernel.
// Declared in gpu/cuda/preprocess.cu, linked via the gpu package's cgo directives.
extern cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream);

// p210_to_rgb_chw is the fused P210 (10-bit 4:2:2) -> RGB float32 CHW preprocessing kernel.
extern cudaError_t p210_to_rgb_chw(
    float* rgbOut,
    const uint16_t* p210,
    int srcW, int srcH, int srcPitch16,
    int outW, int outH,
    cudaStream_t stream);

// sam_threshold_mask converts float32 logits to uint8 binary mask.
// Each pixel: output = (logits[i] > threshold) ? 255 : 0.
// Declared in gpu/cuda/preprocess.cu.
extern cudaError_t sam_threshold_mask(
    uint8_t* maskOut,
    const float* logits,
    int width, int height,
    float threshold,
    cudaStream_t stream);

// mask_to_u8_upscale bilinearly upscales a float32 mask to uint8 at target resolution.
// Declared in gpu/cuda/preprocess.cu.
extern cudaError_t mask_to_u8_upscale(
    uint8_t* dst, int dstW, int dstH,
    const float* src, int srcW, int srcH,
    cudaStream_t stream);

static cudaError_t sam_cuda_malloc(void** ptr, size_t size) {
    return cudaMalloc(ptr, size);
}

static void sam_cuda_free(void* ptr) {
    cudaFree(ptr);
}

static cudaError_t sam_cuda_memset(void* ptr, int value, size_t size) {
    return cudaMemset(ptr, value, size);
}

static cudaError_t sam_cuda_stream_sync(cudaStream_t stream) {
    return cudaStreamSynchronize(stream);
}
*/
import "C"

// SAM 2.1 Tiny model dimensions.
const (
	samEncoderInputW = 1024
	samEncoderInputH = 1024

	// Encoder outputs
	samEmbedDim = 256 // image_embed: [1, 256, 64, 64]
	samEmbedH   = 64
	samEmbedW   = 64
	samHR0Chans = 32  // high_res_feat0: [1, 32, 256, 256]
	samHR0H     = 256
	samHR0W     = 256
	samHR1Chans = 64  // high_res_feat1: [1, 64, 128, 128]
	samHR1H     = 128
	samHR1W     = 128

	// Decoder output
	samMaskH = 256 // masks: [1, 1, 256, 256]
	samMaskW = 256

	samMaxPrompts    = 16  // maximum concurrent prompts
	samMaskThreshold = 0.0 // logit threshold for binary mask
)

// sam2Model wraps SAM 2.1 Tiny for interactive segmentation using TensorRT.
//
// SAM 2.1 operates in two phases:
//  1. Image Encoder (Hiera Tiny): NV12 frame -> 3 feature maps (run once per new scene)
//     - image_embed:    [1, 256, 64, 64]   (main image embedding)
//     - high_res_feat0: [1, 32, 256, 256]   (high-res feature map 0)
//     - high_res_feat1: [1, 64, 128, 128]   (high-res feature map 1)
//  2. Mask Decoder: features + prompts -> segmentation mask (per frame)
//     - Inputs: image_embed, high_res_feat0, high_res_feat1, point_coords, point_labels
//     - Outputs: masks [1,1,256,256], iou_predictions [1,1]
type sam2Model struct {
	encoder    *gpu.TRTEngine
	encoderCtx *gpu.TRTContext
	decoder    *gpu.TRTEngine
	decoderCtx *gpu.TRTContext

	// GPU buffers owned by this model
	encoderInputBuf unsafe.Pointer // float32 RGB CHW: 1×3×1024×1024×4 = 12,582,912 bytes
	imageEmbedBuf   unsafe.Pointer // float32: 1×256×64×64×4 = 4,194,304 bytes
	highResFeat0Buf unsafe.Pointer // float32: 1×32×256×256×4 = 8,388,608 bytes
	highResFeat1Buf unsafe.Pointer // float32: 1×64×128×128×4 = 4,194,304 bytes
	promptCoordsBuf unsafe.Pointer // float32 prompt coordinates: 1×N×2×4 bytes
	promptLabelsBuf unsafe.Pointer // int64 prompt labels: 1×N×8 bytes
	decoderOutBuf   unsafe.Pointer // float32 mask logits: 1×1×256×256×4 = 262,144 bytes
	iouBuf          unsafe.Pointer // float32 IoU predictions: 1×1×4 = 4 bytes
	thresholdBuf    unsafe.Pointer // uint8 thresholded mask at decoder resolution (256×256)
	maskOutputBuf   unsafe.Pointer // uint8 final mask upscaled to frame resolution

	// maskOutputW/H track the dimensions of the maskOutputBuf allocation.
	// Reallocated in Propagate if the frame size changes.
	maskOutputW int
	maskOutputH int

	encoded atomic.Bool // true after first successful Encode()

	mu      sync.RWMutex
	prompts []SAMPrompt
}

// newSAM2Model loads pre-built TensorRT plans for the SAM 2.1 encoder and decoder.
// encoderPlanPath points to the image encoder .plan file.
// decoderPlanPath points to the mask decoder .plan file.
func newSAM2Model(encoderPlanPath, decoderPlanPath string) (*sam2Model, error) {
	encoder, err := gpu.LoadTRTPlan(encoderPlanPath)
	if err != nil {
		return nil, fmt.Errorf("load SAM encoder: %w", err)
	}

	encoderCtx, err := encoder.NewContext()
	if err != nil {
		encoder.Close()
		return nil, fmt.Errorf("create SAM encoder context: %w", err)
	}

	decoder, err := gpu.LoadTRTPlan(decoderPlanPath)
	if err != nil {
		encoderCtx.Close()
		encoder.Close()
		return nil, fmt.Errorf("load SAM decoder: %w", err)
	}

	decoderCtx, err := decoder.NewContext()
	if err != nil {
		decoder.Close()
		encoderCtx.Close()
		encoder.Close()
		return nil, fmt.Errorf("create SAM decoder context: %w", err)
	}

	m := &sam2Model{
		encoder:    encoder,
		encoderCtx: encoderCtx,
		decoder:    decoder,
		decoderCtx: decoderCtx,
	}

	if err := m.allocBuffers(); err != nil {
		m.Close()
		return nil, err
	}

	return m, nil
}

// samCudaAlloc allocates a GPU buffer via cudaMalloc.
// Uses a local variable to avoid passing a Go struct field pointer to C
// (which would trigger cgo's pointer checker for unsafe.Pointer fields).
func samCudaAlloc(size int, name string) (unsafe.Pointer, error) {
	var ptr unsafe.Pointer
	sz := C.size_t(size)
	if rc := C.sam_cuda_malloc(&ptr, sz); rc != C.cudaSuccess {
		return nil, fmt.Errorf("alloc SAM %s (%d bytes): CUDA error %d", name, size, rc)
	}
	return ptr, nil
}

// allocBuffers allocates all GPU buffers for the SAM 2.1 pipeline.
func (m *sam2Model) allocBuffers() error {
	var err error

	// Encoder input: 1×3×1024×1024 float32
	if m.encoderInputBuf, err = samCudaAlloc(samEncoderInputW*samEncoderInputH*3*4, "encoder input"); err != nil {
		return err
	}
	// Encoder outputs (3 feature maps)
	if m.imageEmbedBuf, err = samCudaAlloc(samEmbedDim*samEmbedH*samEmbedW*4, "image embeddings"); err != nil {
		return err
	}
	if m.highResFeat0Buf, err = samCudaAlloc(samHR0Chans*samHR0H*samHR0W*4, "high_res_feat0"); err != nil {
		return err
	}
	if m.highResFeat1Buf, err = samCudaAlloc(samHR1Chans*samHR1H*samHR1W*4, "high_res_feat1"); err != nil {
		return err
	}
	// Decoder inputs: prompts
	if m.promptCoordsBuf, err = samCudaAlloc(samMaxPrompts*2*4, "prompt coords"); err != nil {
		return err
	}
	if m.promptLabelsBuf, err = samCudaAlloc(samMaxPrompts*8, "prompt labels"); err != nil {
		return err
	}
	// Decoder outputs
	if m.decoderOutBuf, err = samCudaAlloc(samMaskH*samMaskW*4, "decoder output"); err != nil {
		return err
	}
	if m.iouBuf, err = samCudaAlloc(4, "iou predictions"); err != nil {
		return err
	}
	if m.thresholdBuf, err = samCudaAlloc(samMaskH*samMaskW, "threshold mask"); err != nil {
		return err
	}
	return nil
}

// Encode runs the SAM 2.1 image encoder on an NV12 GPU frame.
//
// This produces 3 feature maps that persist until the next Encode() call:
//   - image_embed:    [1, 256, 64, 64]
//   - high_res_feat0: [1, 32, 256, 256]
//   - high_res_feat1: [1, 64, 128, 128]
//
// Typically called once when the scene changes or when prompts are first added.
func (m *sam2Model) Encode(inputPtr uintptr, w, h, pitch int, format FrameFormat) error {
	if m == nil || m.encoder == nil {
		return ErrDisabled
	}

	// Preprocess frame -> RGB float32 CHW, resize to 1024x1024.
	var rc C.cudaError_t
	switch format {
	case FormatP210:
		if pitch%2 != 0 {
			return fmt.Errorf("SAM encoder preprocess P210: odd pitch %d", pitch)
		}
		rc = C.p210_to_rgb_chw(
			(*C.float)(m.encoderInputBuf),
			(*C.uint16_t)(unsafe.Pointer(inputPtr)),
			C.int(w), C.int(h), C.int(pitch/2),
			C.int(samEncoderInputW), C.int(samEncoderInputH),
			nil,
		)
		if rc != C.cudaSuccess {
			return fmt.Errorf("SAM encoder preprocess P210->RGB: CUDA error %d", rc)
		}
	default:
		rc = C.nv12_to_rgb_chw(
			(*C.float)(m.encoderInputBuf),
			(*C.uint8_t)(unsafe.Pointer(inputPtr)),
			C.int(w), C.int(h), C.int(pitch),
			C.int(samEncoderInputW), C.int(samEncoderInputH),
			nil,
		)
		if rc != C.cudaSuccess {
			return fmt.Errorf("SAM encoder preprocess NV12->RGB: CUDA error %d", rc)
		}
	}

	// Run image encoder with named bindings: 1 input, 3 outputs.
	bindings := []gpu.TRTBinding{
		{Name: "image", DevPtr: m.encoderInputBuf, Dims: []int{1, 3, samEncoderInputH, samEncoderInputW}},
		{Name: "image_embed", DevPtr: m.imageEmbedBuf, Dims: []int{1, samEmbedDim, samEmbedH, samEmbedW}},
		{Name: "high_res_feat0", DevPtr: m.highResFeat0Buf, Dims: []int{1, samHR0Chans, samHR0H, samHR0W}},
		{Name: "high_res_feat1", DevPtr: m.highResFeat1Buf, Dims: []int{1, samHR1Chans, samHR1H, samHR1W}},
	}

	if err := m.encoderCtx.InferMulti(bindings, nil); err != nil {
		return fmt.Errorf("SAM encoder inference: %w", err)
	}

	if rc := C.sam_cuda_stream_sync(nil); rc != C.cudaSuccess {
		return fmt.Errorf("SAM encoder stream sync: CUDA error %d", rc)
	}

	m.encoded.Store(true)
	return nil
}

// Propagate runs the SAM 2.1 mask decoder and writes a uint8 binary mask.
//
// inputPtr is the NV12 frame (used for coordinate normalization context).
// outputPtr, if non-zero, receives the uint8 per-pixel mask at the input
// resolution via a device-to-device copy. When outputPtr is 0, the result
// is left in the internal maskOutputBuf accessible via MaskOutputPtr().
// Encode() must have been called at least once before Propagate().
func (m *sam2Model) Propagate(inputPtr uintptr, w, h, pitch int, outputPtr uintptr, format FrameFormat) error {
	if m == nil || m.decoder == nil {
		return ErrDisabled
	}
	if !m.encoded.Load() {
		return fmt.Errorf("%w: Encode() must be called before Propagate()", ErrNotReady)
	}

	// Ensure the mask output buffer is allocated at the correct frame resolution.
	if err := m.ensureMaskOutputBuf(w, h); err != nil {
		return err
	}

	m.mu.RLock()
	numPrompts := len(m.prompts)
	m.mu.RUnlock()

	if numPrompts == 0 {
		// No prompts: zero the internal mask output buffer.
		maskBytes := C.size_t(w * h)
		if rc := C.sam_cuda_memset(m.maskOutputBuf, 0, maskBytes); rc != C.cudaSuccess {
			return fmt.Errorf("SAM zero output mask: CUDA error %d", rc)
		}
		return nil
	}

	// Prepare prompt tensors on GPU.
	if err := m.preparePromptTensors(w, h); err != nil {
		return fmt.Errorf("SAM prepare prompts: %w", err)
	}

	// Run mask decoder with multi-binding inference.
	// Inputs: image_embed, high_res_feat0, high_res_feat1, point_coords, point_labels
	// Outputs: masks (256x256 logits), iou_predictions
	bindings := []gpu.TRTBinding{
		{Name: "image_embed", DevPtr: m.imageEmbedBuf, Dims: []int{1, samEmbedDim, samEmbedH, samEmbedW}},
		{Name: "high_res_feat0", DevPtr: m.highResFeat0Buf, Dims: []int{1, samHR0Chans, samHR0H, samHR0W}},
		{Name: "high_res_feat1", DevPtr: m.highResFeat1Buf, Dims: []int{1, samHR1Chans, samHR1H, samHR1W}},
		{Name: "point_coords", DevPtr: m.promptCoordsBuf, Dims: []int{1, numPrompts, 2}},
		{Name: "point_labels", DevPtr: m.promptLabelsBuf, Dims: []int{1, numPrompts}},
		{Name: "masks", DevPtr: m.decoderOutBuf, Dims: []int{1, 1, samMaskH, samMaskW}},
		{Name: "iou_predictions", DevPtr: m.iouBuf, Dims: []int{1, 1}},
	}

	if err := m.decoderCtx.InferMulti(bindings, nil); err != nil {
		return fmt.Errorf("SAM decoder inference: %w", err)
	}

	if rc := C.sam_cuda_stream_sync(nil); rc != C.cudaSuccess {
		return fmt.Errorf("SAM decoder stream sync: CUDA error %d", rc)
	}

	// Upscale the decoder's 256x256 logits to the frame resolution.
	// mask_to_u8_upscale does bilinear interpolation of float32 logits and
	// thresholds to uint8 (0 or 255) in a single pass for smooth edges.
	rc := C.mask_to_u8_upscale(
		(*C.uint8_t)(m.maskOutputBuf),
		C.int(w), C.int(h),
		(*C.float)(m.decoderOutBuf),
		C.int(samMaskW), C.int(samMaskH),
		nil,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("SAM upscale mask: CUDA error %d", rc)
	}

	if rc := C.sam_cuda_stream_sync(nil); rc != C.cudaSuccess {
		return fmt.Errorf("SAM mask sync: CUDA error %d", rc)
	}

	// Copy to caller's buffer if a separate output pointer was provided.
	if outputPtr != 0 && outputPtr != uintptr(m.maskOutputBuf) {
		maskBytes := C.size_t(w * h)
		if rc := C.cudaMemcpy(
			unsafe.Pointer(outputPtr),
			m.maskOutputBuf,
			maskBytes,
			C.cudaMemcpyDeviceToDevice,
		); rc != C.cudaSuccess {
			return fmt.Errorf("SAM copy output mask: CUDA error %d", rc)
		}
	}

	return nil
}

// preparePromptTensors uploads prompt coordinates and labels to GPU buffers.
// Coordinates are normalized to the encoder input resolution (1024x1024).
// Labels are uploaded as int64 (matching SAM 2.1's expected dtype).
func (m *sam2Model) preparePromptTensors(frameW, frameH int) error {
	m.mu.RLock()
	prompts := make([]SAMPrompt, len(m.prompts))
	copy(prompts, m.prompts)
	m.mu.RUnlock()

	if len(prompts) == 0 {
		return nil
	}
	if len(prompts) > samMaxPrompts {
		prompts = prompts[:samMaxPrompts]
	}

	// Build coordinate (float32) and label (int64) arrays on CPU, then upload.
	coords := make([]float32, len(prompts)*2)
	labels := make([]int64, len(prompts))

	scaleX := float32(samEncoderInputW) / float32(frameW)
	scaleY := float32(samEncoderInputH) / float32(frameH)

	for i, p := range prompts {
		switch p.Type {
		case "point":
			if len(p.Points) > 0 {
				coords[i*2] = float32(p.Points[0][0]) * scaleX
				coords[i*2+1] = float32(p.Points[0][1]) * scaleY
			}
			labels[i] = 1 // foreground
		case "box":
			// For box prompts, use center point.
			cx := float32(p.Box[0]+p.Box[2]) / 2.0 * scaleX
			cy := float32(p.Box[1]+p.Box[3]) / 2.0 * scaleY
			coords[i*2] = cx
			coords[i*2+1] = cy
			labels[i] = 1 // foreground
		}
	}

	// Upload coords (float32).
	coordBytes := C.size_t(len(coords) * 4)
	if rc := C.cudaMemcpy(
		m.promptCoordsBuf,
		unsafe.Pointer(&coords[0]),
		coordBytes,
		C.cudaMemcpyHostToDevice,
	); rc != C.cudaSuccess {
		return fmt.Errorf("upload prompt coords: CUDA error %d", rc)
	}

	// Upload labels (int64).
	labelBytes := C.size_t(len(labels) * 8)
	if rc := C.cudaMemcpy(
		m.promptLabelsBuf,
		unsafe.Pointer(&labels[0]),
		labelBytes,
		C.cudaMemcpyHostToDevice,
	); rc != C.cudaSuccess {
		return fmt.Errorf("upload prompt labels: CUDA error %d", rc)
	}

	return nil
}

// MaskOutputPtr returns the CUDA device pointer to the model's internal
// mask output buffer (uint8 binary mask at frame resolution).
// Returns 0 if the model is nil or no mask has been computed yet.
func (m *sam2Model) MaskOutputPtr() uintptr {
	if m == nil || m.maskOutputBuf == nil {
		return 0
	}
	return uintptr(m.maskOutputBuf)
}

// ensureMaskOutputBuf ensures the maskOutputBuf is allocated at the given
// frame dimensions. Reallocates if the size changes.
func (m *sam2Model) ensureMaskOutputBuf(w, h int) error {
	if m.maskOutputBuf != nil && m.maskOutputW == w && m.maskOutputH == h {
		return nil // already the right size
	}
	// Free old buffer if dimensions changed.
	if m.maskOutputBuf != nil {
		C.sam_cuda_free(m.maskOutputBuf)
		m.maskOutputBuf = nil
	}
	var err error
	m.maskOutputBuf, err = samCudaAlloc(w*h, "mask output")
	if err != nil {
		return fmt.Errorf("alloc SAM mask output (%dx%d): %w", w, h, err)
	}
	m.maskOutputW = w
	m.maskOutputH = h
	return nil
}

// AddPrompt registers a new segmentation prompt.
// Returns an error if the ID is empty or already in use.
func (m *sam2Model) AddPrompt(p SAMPrompt) error {
	if m == nil {
		return ErrDisabled
	}
	if p.ID == "" {
		return fmt.Errorf("%w: prompt ID must not be empty", ErrPromptFailed)
	}
	if p.Type != "point" && p.Type != "box" {
		return fmt.Errorf("%w: unsupported prompt type %q", ErrPromptFailed, p.Type)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.prompts {
		if existing.ID == p.ID {
			return fmt.Errorf("%w: duplicate prompt ID %q", ErrPromptFailed, p.ID)
		}
	}

	if len(m.prompts) >= samMaxPrompts {
		return fmt.Errorf("%w: maximum %d prompts reached", ErrPromptFailed, samMaxPrompts)
	}

	m.prompts = append(m.prompts, p)
	return nil
}

// RemovePrompt removes a prompt by ID.
// Returns an error if the prompt is not found.
func (m *sam2Model) RemovePrompt(id string) error {
	if m == nil {
		return ErrDisabled
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.prompts {
		if p.ID == id {
			m.prompts = append(m.prompts[:i], m.prompts[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("%w: prompt %q not found", ErrPromptFailed, id)
}

// Prompts returns a copy of all active prompts.
func (m *sam2Model) Prompts() []SAMPrompt {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.prompts) == 0 {
		return nil
	}

	out := make([]SAMPrompt, len(m.prompts))
	copy(out, m.prompts)
	return out
}

// PreallocMask pre-allocates the mask output buffer at the given resolution
// and returns the device pointer. Returns 0 on error or nil receiver.
func (m *sam2Model) PreallocMask(w, h int) uintptr {
	if m == nil {
		return 0
	}
	if err := m.ensureMaskOutputBuf(w, h); err != nil {
		return 0
	}
	return m.MaskOutputPtr()
}

// Close releases all GPU resources held by the SAM model.
// Safe to call on a nil receiver or multiple times.
func (m *sam2Model) Close() {
	if m == nil {
		return
	}

	bufs := []unsafe.Pointer{
		m.encoderInputBuf,
		m.imageEmbedBuf,
		m.highResFeat0Buf,
		m.highResFeat1Buf,
		m.promptCoordsBuf,
		m.promptLabelsBuf,
		m.decoderOutBuf,
		m.iouBuf,
		m.thresholdBuf,
		m.maskOutputBuf,
	}
	for _, buf := range bufs {
		if buf != nil {
			C.sam_cuda_free(buf)
		}
	}
	m.encoderInputBuf = nil
	m.imageEmbedBuf = nil
	m.highResFeat0Buf = nil
	m.highResFeat1Buf = nil
	m.promptCoordsBuf = nil
	m.promptLabelsBuf = nil
	m.decoderOutBuf = nil
	m.iouBuf = nil
	m.thresholdBuf = nil
	m.maskOutputBuf = nil
	m.maskOutputW = 0
	m.maskOutputH = 0

	if m.decoderCtx != nil {
		m.decoderCtx.Close()
		m.decoderCtx = nil
	}
	if m.decoder != nil {
		m.decoder.Close()
		m.decoder = nil
	}
	if m.encoderCtx != nil {
		m.encoderCtx.Close()
		m.encoderCtx = nil
	}
	if m.encoder != nil {
		m.encoder.Close()
		m.encoder = nil
	}

	m.encoded.Store(false)
}
