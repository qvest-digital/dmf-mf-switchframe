//go:build !cuda || !cgo

package sidecar

// InitCUDADriver is a no-op on non-CUDA builds.
func InitCUDADriver() error {
	return ErrCUDANotAvailable
}
