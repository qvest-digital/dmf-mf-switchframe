//go:build cgo && cuda && tensorrt

package gpu

import (
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

// errInvalidHexColor is returned when a color:RRGGBB string is malformed.
var errInvalidHexColor = errors.New("gpu: ai_segment: invalid hex color (expected RRGGBB)")

// gpuAISegmentNode applies AI-based person segmentation and background
// replacement on the GPU. It reads segmentation state via the
// SegmentationState interface, retrieves per-source masks produced by the
// SegmentationEngine, and composites the person onto a replacement
// background (blur, solid color, or transparent pass-through).
//
// The node sits in the pipeline after the DSK compositor and before the
// ST map node, so it processes the composited program frame.
type gpuAISegmentNode struct {
	ctx   *Context
	pool  *FramePool
	state SegmentationState

	// Pre-allocated scratch frame for background compositing.
	blurTmp *GPUFrame

	width, height int
	lastErr       atomic.Value
}

// NewGPUAISegmentNode creates a GPU AI segmentation pipeline node.
// The state parameter provides segmentation masks and per-source configs.
// Returns nil if state is nil (no segmentation engine configured).
func NewGPUAISegmentNode(ctx *Context, pool *FramePool, state SegmentationState) GPUPipelineNode {
	if state == nil {
		return nil
	}
	return &gpuAISegmentNode{
		ctx:   ctx,
		pool:  pool,
		state: state,
	}
}

func (n *gpuAISegmentNode) Name() string { return "gpu_ai_segment" }

func (n *gpuAISegmentNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height

	// Allocate or reallocate scratch buffer for background compositing.
	if n.blurTmp != nil {
		n.blurTmp.Release()
		n.blurTmp = nil
	}
	tmp, err := n.pool.Acquire()
	if err != nil {
		return err
	}
	n.blurTmp = tmp

	return nil
}

// Active returns true when a segmentation state provider is attached and
// at least one source has segmentation enabled. The HasEnabledSources()
// check is the fast-path optimization — when no sources are enabled,
// ProcessGPU returns immediately.
func (n *gpuAISegmentNode) Active() bool {
	return n.state != nil && n.state.HasEnabledSources()
}

func (n *gpuAISegmentNode) Latency() time.Duration { return 200 * time.Microsecond }

func (n *gpuAISegmentNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// ProcessGPU applies AI segmentation to the program frame.
//
//  1. If no sources have segmentation enabled, return immediately (fast path).
//  2. Get the current program source key.
//  3. Retrieve the pre-computed mask for that source.
//  4. Apply background replacement based on the source's config.
//
// Background modes:
//   - "transparent" or "": no-op pass-through (mask available for downstream use)
//   - "blur:N": GPU blur the frame -> composite person (mask) on blurred bg
//   - "color:RRGGBB": GPU fill with solid color -> composite person on color bg
//
// Note: blur and color modes require CUDA kernels from Phase 3.2. Until those
// are implemented, those modes log a warning and pass through unchanged.
func (n *gpuAISegmentNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil {
		return nil
	}

	// Fast path: no sources have segmentation enabled.
	if !n.state.HasEnabledSources() {
		return nil
	}

	// Get the current program source key.
	programKey := n.state.ProgramSourceKey()
	if programKey == "" {
		return nil
	}

	// Get the pre-computed mask for the program source.
	mask := n.state.MaskForSource(programKey)
	if mask == nil {
		// No mask produced yet (inference hasn't completed), pass through.
		return nil
	}

	// Get the configuration for this source.
	config := n.state.ConfigForSource(programKey)
	if config == nil {
		return nil
	}

	// Apply background replacement based on config.
	bg := config.Background
	if bg == "" || bg == "transparent" {
		// Transparent mode: the mask is available but we don't modify the
		// program frame. This mode is useful when the segmented source is
		// used as a fill for upstream keying (the mask serves as the key).
		return nil
	}

	// For blur and color modes, we need to:
	// 1. Create the replacement background in blurTmp
	// 2. Composite the person (via mask) from the original frame onto blurTmp
	// 3. Copy the result back to frame

	if err := n.applyBackground(frame, mask, bg); err != nil {
		slog.Warn("gpu_ai_segment: background replacement failed",
			"source", programKey, "background", bg, "error", err)
		n.lastErr.Store(err)
		// Don't fail the pipeline — pass through the unmodified frame.
	}

	return nil
}

// applyBackground dispatches to the appropriate background replacement kernel.
func (n *gpuAISegmentNode) applyBackground(frame, mask *GPUFrame, bg string) error {
	switch {
	case len(bg) > 5 && bg[:5] == "blur:":
		// Gaussian blur mode: blur the entire frame, then composite person.
		// Phase 3.2 will implement the GPU Gaussian blur kernel.
		// For now, copy frame to blurTmp and composite using mask —
		// this demonstrates the compositing path works (with identity blur).
		if err := CopyGPUFrame(n.blurTmp, frame); err != nil {
			return err
		}

		// TODO(phase-3.2): Apply GPU Gaussian blur to blurTmp here.
		// Parse radius: blur:N where N is the kernel radius.

		// Composite: dst[i] = lerp(blurTmp[i], frame[i], mask[i])
		// BlendStinger reads mask Y-plane as per-pixel alpha (255=foreground).
		// base = blurTmp (blurred background), overlay = frame (person)
		return BlendStinger(n.ctx, frame, n.blurTmp, frame, mask)

	case len(bg) > 6 && bg[:6] == "color:":
		// Solid color mode: fill blurTmp with color, then composite person.
		// Phase 3.2 will implement FillSolidColor CUDA kernel.
		// For now, use BlendFTB at full position to fill with black, which
		// demonstrates the path. Proper color fill comes with the kernel.
		slog.Debug("gpu_ai_segment: color mode placeholder (using black until Phase 3.2 kernel)")

		// Fill blurTmp with black (Y=16, Cb=128, Cr=128 limited range).
		if err := BlendFTB(n.ctx, n.blurTmp, frame, 1.0); err != nil {
			return err
		}

		// Composite: person from original frame onto color background.
		return BlendStinger(n.ctx, frame, n.blurTmp, frame, mask)

	default:
		// Unknown mode — treat as transparent (no-op).
		slog.Warn("gpu_ai_segment: unknown background mode, treating as transparent",
			"background", bg)
		return nil
	}
}

func (n *gpuAISegmentNode) Close() error {
	if n.blurTmp != nil {
		n.blurTmp.Release()
		n.blurTmp = nil
	}
	return nil
}

// parseHexColorToYCbCr converts an RRGGBB hex string to BT.709 YCbCr (limited range).
func parseHexColorToYCbCr(hex string) (y, cb, cr uint8, err error) {
	if len(hex) != 6 {
		return 0, 128, 128, errInvalidHexColor
	}
	r, err := parseHexByte(hex[0:2])
	if err != nil {
		return 0, 128, 128, errInvalidHexColor
	}
	g, err := parseHexByte(hex[2:4])
	if err != nil {
		return 0, 128, 128, errInvalidHexColor
	}
	b, err := parseHexByte(hex[4:6])
	if err != nil {
		return 0, 128, 128, errInvalidHexColor
	}

	// BT.709 limited range conversion.
	rf, gf, bf := float64(r)/255.0, float64(g)/255.0, float64(b)/255.0
	yf := 16.0 + 65.481*rf + 128.553*gf + 24.966*bf
	cbf := 128.0 - 37.797*rf - 74.203*gf + 112.0*bf
	crf := 128.0 + 112.0*rf - 93.786*gf - 18.214*bf

	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}

	return clamp(yf), clamp(cbf), clamp(crf), nil
}

// parseHexByte parses a 2-character hex string into a byte value.
func parseHexByte(s string) (byte, error) {
	if len(s) != 2 {
		return 0, errInvalidHexColor
	}
	hi, ok1 := hexDigit(s[0])
	lo, ok2 := hexDigit(s[1])
	if !ok1 || !ok2 {
		return 0, errInvalidHexColor
	}
	return hi<<4 | lo, nil
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
