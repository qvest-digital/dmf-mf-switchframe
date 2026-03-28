//go:build !cgo || !cuda

package tracking

import (
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

// VisualTrackingProxy is a stub for non-CUDA builds.
type VisualTrackingProxy struct{}

// Compile-time check that stub satisfies TrackingSource.
var _ TrackingSource = (*VisualTrackingProxy)(nil)

// NewVisualTrackingProxy returns nil on non-CUDA builds.
func NewVisualTrackingProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool, sourceKey string, framePool *gpu.FramePool) *VisualTrackingProxy {
	return nil
}

func (p *VisualTrackingProxy) Name() string            { return "visual-stub" }
func (p *VisualTrackingProxy) Type() string            { return "visual" }
func (p *VisualTrackingProxy) Start() error            { return nil }
func (p *VisualTrackingProxy) Stop() error             { return nil }
func (p *VisualTrackingProxy) Running() bool           { return false }
func (p *VisualTrackingProxy) LatestPose() *CameraPose { return nil }
func (p *VisualTrackingProxy) ProcessFrame(framePtr uintptr, w, h, pitch int) {}
func (p *VisualTrackingProxy) HandleMessage(msg *sidecar.Message) {}
func (p *VisualTrackingProxy) RecoverStaleSlots()                 {}
func (p *VisualTrackingProxy) Close() error                       { return nil }
