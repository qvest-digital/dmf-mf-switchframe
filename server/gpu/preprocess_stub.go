//go:build (!cgo || !cuda) && !darwin

package gpu

import "unsafe"

// PreprocessNV12ToRGB returns ErrGPUNotAvailable on non-GPU builds.
func PreprocessNV12ToRGB(ctx *Context, rgbOut unsafe.Pointer, src *GPUFrame, outW, outH int) error {
	return ErrGPUNotAvailable
}

// PreprocessNV12ToRGBNHWC returns ErrGPUNotAvailable on non-GPU builds.
func PreprocessNV12ToRGBNHWC(ctx *Context, rgbOut unsafe.Pointer, src *GPUFrame, outW, outH int) error {
	return ErrGPUNotAvailable
}

// AllocRGBBuffer returns ErrGPUNotAvailable on non-GPU builds.
func AllocRGBBuffer(outW, outH int) (unsafe.Pointer, error) {
	return nil, ErrGPUNotAvailable
}

// FreeRGBBuffer is a no-op on non-GPU builds.
func FreeRGBBuffer(buf unsafe.Pointer) {}

// DownloadRGBBuffer returns ErrGPUNotAvailable on non-GPU builds.
func DownloadRGBBuffer(dst []float32, devPtr unsafe.Pointer, outW, outH int) error {
	return ErrGPUNotAvailable
}

// DownloadMaskU8 returns ErrGPUNotAvailable on non-GPU builds.
func DownloadMaskU8(dst []byte, devPtr unsafe.Pointer, size int) error {
	return ErrGPUNotAvailable
}

// AllocDeviceBytes returns ErrGPUNotAvailable on non-GPU builds.
func AllocDeviceBytes(size int) (unsafe.Pointer, error) {
	return nil, ErrGPUNotAvailable
}

// FreeDeviceBytes is a no-op on non-GPU builds.
func FreeDeviceBytes(ptr unsafe.Pointer) {}

// UploadBytes returns ErrGPUNotAvailable on non-GPU builds.
func UploadBytes(devPtr unsafe.Pointer, data []byte) error {
	return ErrGPUNotAvailable
}

// MaskFloatToU8Upscale returns ErrGPUNotAvailable on non-GPU builds.
func MaskFloatToU8Upscale(dstPtr unsafe.Pointer, dstW, dstH int, srcPtr unsafe.Pointer, srcW, srcH int, stream uintptr) error {
	return ErrGPUNotAvailable
}

// MaskEMA returns ErrGPUNotAvailable on non-GPU builds.
func MaskEMA(output, prev, curr unsafe.Pointer, alpha float32, size int, stream uintptr) error {
	return ErrGPUNotAvailable
}

// MaskErode3x3 returns ErrGPUNotAvailable on non-GPU builds.
func MaskErode3x3(dst, src unsafe.Pointer, width, height int, stream uintptr) error {
	return ErrGPUNotAvailable
}
