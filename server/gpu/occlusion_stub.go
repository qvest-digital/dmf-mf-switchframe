//go:build !cgo || !cuda

package gpu

// OcclusionFuseParams controls the depth-to-occlusion mapping.
type OcclusionFuseParams struct {
	DepthNear float32 // depth threshold for foreground (default 0.3)
	Softness  float32 // transition softness (default 0.1, range 0.01-0.5)
}

// DefaultOcclusionFuseParams returns sensible defaults for occlusion fusion.
func DefaultOcclusionFuseParams() OcclusionFuseParams {
	return OcclusionFuseParams{
		DepthNear: 0.3,
		Softness:  0.1,
	}
}

// FuseOcclusion returns ErrGPUNotAvailable on non-GPU builds.
func FuseOcclusion(ctx *Context, output, depth, samMask uintptr,
	width, height, depthW, depthH int, params OcclusionFuseParams) error {
	return ErrGPUNotAvailable
}
