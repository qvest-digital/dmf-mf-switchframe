//go:build !cgo || !cuda

package gpu

// ColorGradeLUT3D returns ErrGPUNotAvailable on non-GPU builds.
func ColorGradeLUT3D(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	return ErrGPUNotAvailable
}

// UploadLUT3D returns ErrGPUNotAvailable on non-GPU builds.
func UploadLUT3D(data []float32) (uintptr, error) {
	return 0, ErrGPUNotAvailable
}

// FreeLUT3D is a no-op on non-GPU builds.
func FreeLUT3D(devPtr uintptr) {}

// ColorGradeLUT3DP210 returns ErrGPUNotAvailable on non-GPU builds.
func ColorGradeLUT3DP210(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	return ErrGPUNotAvailable
}
