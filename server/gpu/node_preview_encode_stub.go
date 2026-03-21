//go:build (!cgo || !cuda) && !darwin

package gpu

// NewGPUPreviewEncodeNode returns nil on non-GPU builds.
func NewGPUPreviewEncodeNode(ctx *Context, previewW, previewH, bitrate, fpsNum, fpsDen int,
	onEncoded func(data []byte, isIDR bool, pts int64)) GPUPipelineNode {
	return nil
}
