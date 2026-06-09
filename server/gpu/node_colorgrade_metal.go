//go:build cgo && darwin

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuColorGradeNode implements GPUPipelineNode for 3D LUT color grading.
// It reads color grade state via the ColorGradeState interface, uploads
// the LUT to GPU memory on change (generation counter cache), and dispatches
// the color grading kernel each frame.
type gpuColorGradeNode struct {
	ctx   *Context
	state ColorGradeState

	// Cached LUT on GPU device memory.
	lutDev  uintptr // Metal buffer pointer to float32 LUT data
	lutGen  uint64  // cached generation counter
	lutSize int     // cube dimension

	width, height, pitch int
	lastErr              atomic.Value
}

// NewGPUColorGradeNode creates a GPU color grade pipeline node.
// Pass nil state to disable.
func NewGPUColorGradeNode(ctx *Context, state ColorGradeState) GPUPipelineNode {
	if state == nil {
		return nil
	}
	return &gpuColorGradeNode{
		ctx:   ctx,
		state: state,
	}
}

func (n *gpuColorGradeNode) Name() string { return "gpu_colorgrade" }

func (n *gpuColorGradeNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	n.pitch = pitch
	return nil
}

func (n *gpuColorGradeNode) Active() bool {
	return n.state != nil && n.state.HasGradingEnabled()
}

func (n *gpuColorGradeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuColorGradeNode) Latency() time.Duration { return 500 * time.Microsecond }

func (n *gpuColorGradeNode) Close() error {
	if n.lutDev != 0 {
		FreeLUT3D(n.lutDev)
		n.lutDev = 0
		n.lutGen = 0
		n.lutSize = 0
	}
	return nil
}

func (n *gpuColorGradeNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil || !n.state.HasGradingEnabled() {
		return nil
	}

	intensity := n.state.GradingIntensity()
	if intensity <= 0 {
		return nil
	}

	// Check if we need to upload a new LUT.
	gen := n.state.GradingLUTGen()
	if n.lutDev == 0 || gen != n.lutGen {
		// Free old LUT.
		if n.lutDev != 0 {
			FreeLUT3D(n.lutDev)
			n.lutDev = 0
		}

		data := n.state.GradingLUTData()
		if len(data) == 0 {
			return nil
		}

		// Fail closed if the declared cube dimension disagrees with the data
		// length — otherwise the kernel would index past the device buffer.
		size := n.state.GradingLUTSize()
		if err := validateLUT3D(data, size); err != nil {
			n.lastErr.Store(err)
			slog.Warn("gpu_colorgrade: invalid LUT, skipping", "error", err)
			return nil
		}

		devPtr, err := UploadLUT3D(data)
		if err != nil {
			n.lastErr.Store(err)
			slog.Warn("gpu_colorgrade: LUT upload failed, skipping", "error", err)
			return nil
		}
		n.lutDev = devPtr
		n.lutGen = gen
		n.lutSize = size
	}

	// Dispatch to the correct kernel based on frame format.
	var err error
	switch frame.Format {
	case GPUFormatP210:
		err = ColorGradeLUT3DP210(n.ctx, frame, n.lutDev, n.lutSize, intensity)
	default: // GPUFormatNV12
		err = ColorGradeLUT3D(n.ctx, frame, n.lutDev, n.lutSize, intensity)
	}
	if err != nil {
		n.lastErr.Store(err)
		slog.Warn("gpu_colorgrade: kernel failed, skipping", "error", err, "format", frame.Format)
		return nil
	}

	return nil
}
