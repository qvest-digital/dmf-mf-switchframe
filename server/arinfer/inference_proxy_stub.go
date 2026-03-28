//go:build !cgo || !cuda

package arinfer

import (
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

// InferenceProxy is a stub for non-CUDA builds.
type InferenceProxy struct{}

// Compile-time check that stub satisfies Inferrer.
var _ Inferrer = (*InferenceProxy)(nil)

// NewInferenceProxy returns nil on non-CUDA builds.
func NewInferenceProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool, framePool *gpu.FramePool) *InferenceProxy {
	return nil
}

// ProcessFrame is a no-op on non-CUDA builds.
func (p *InferenceProxy) ProcessFrame(framePtr uintptr, w, h, pitch int) {}

// LastOcclusion returns nil on non-CUDA builds.
func (p *InferenceProxy) LastOcclusion() *OcclusionMap { return nil }

// IsActive returns false on non-CUDA builds.
func (p *InferenceProxy) IsActive() bool { return false }

// Hook returns a no-op ProcessFrameHook on non-CUDA builds.
func (p *InferenceProxy) Hook() ProcessFrameHook { return func(uintptr, int, int, int) {} }

// EnableDepth returns ErrDisabled on non-CUDA builds.
func (p *InferenceProxy) EnableDepth(planPath string) error { return ErrDisabled }

// DisableDepth is a no-op on non-CUDA builds.
func (p *InferenceProxy) DisableDepth() {}

// EnableSAM returns ErrDisabled on non-CUDA builds.
func (p *InferenceProxy) EnableSAM(encoderPlanPath, decoderPlanPath string) error {
	return ErrDisabled
}

// DisableSAM is a no-op on non-CUDA builds.
func (p *InferenceProxy) DisableSAM() {}

// AddPrompt returns ErrDisabled on non-CUDA builds.
func (p *InferenceProxy) AddPrompt(p2 SAMPrompt) error { return ErrDisabled }

// RemovePrompt returns ErrDisabled on non-CUDA builds.
func (p *InferenceProxy) RemovePrompt(id string) error { return ErrDisabled }

// Prompts returns nil on non-CUDA builds.
func (p *InferenceProxy) Prompts() []SAMPrompt { return nil }

// Status returns empty status on non-CUDA builds.
func (p *InferenceProxy) Status() Status { return Status{} }

// DepthAtPixel returns default depth on non-CUDA builds.
func (p *InferenceProxy) DepthAtPixel(x, y, frameW, frameH int) (float64, error) {
	return 2.0, nil
}

// HandleMessage is a no-op on non-CUDA builds.
func (p *InferenceProxy) HandleMessage(msg *sidecar.Message) {}

// RecoverStaleSlots is a no-op on non-CUDA builds.
func (p *InferenceProxy) RecoverStaleSlots() {}

// ReenableState is a no-op on non-CUDA builds.
func (p *InferenceProxy) ReenableState() {}

// Close is a no-op on non-CUDA builds.
func (p *InferenceProxy) Close() {}
