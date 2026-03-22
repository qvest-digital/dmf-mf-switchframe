//go:build !cgo || !cuda

package gpu

// SharpenLuma returns ErrGPUNotAvailable on non-GPU builds.
func SharpenLuma(ctx *Context, frame *GPUFrame, amount float32) error {
	return ErrGPUNotAvailable
}
