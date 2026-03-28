//go:build !cgo || (!cuda && !darwin)

package gpu

// NewGPUColorGradeNode returns nil on non-GPU builds.
func NewGPUColorGradeNode(ctx *Context, state ColorGradeState) GPUPipelineNode {
	return nil
}
