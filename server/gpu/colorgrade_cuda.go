//go:build cgo && cuda

package gpu

/*
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declarations for CUDA kernels (implemented in cuda/colorgrade.cu)
cudaError_t launch_colorgrade_lut3d(
    uint8_t* nv12, const float* lut, int lutSize, float intensity,
    int width, int height, int pitch, cudaStream_t stream);
cudaError_t launch_colorgrade_lut3d_p210(
    uint8_t* p210, const float* lut, int lutSize, float intensity,
    int width, int height, int pitch, cudaStream_t stream);
cudaError_t colorgrade_upload_lut(float** devLut, const float* hostLut, int count);
void colorgrade_free_lut(float* devLut);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// ColorGradeLUT3D applies a 3D LUT color grade to an NV12 frame in-place.
// The LUT must already be uploaded to GPU memory via UploadLUT3D.
// intensity controls blend: 0.0 = bypass, 1.0 = full grade.
func ColorGradeLUT3D(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	rc := C.launch_colorgrade_lut3d(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.float)(unsafe.Pointer(lutDevPtr)),
		C.int(lutSize),
		C.float(intensity),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: colorgrade LUT3D failed: %d", rc)
	}
	return ctx.Sync()
}

// ColorGradeLUT3DP210 applies a 3D LUT color grade to a P210 (10-bit 4:2:2)
// frame in-place. Uses 10-bit limited-range BT.709 scaling:
//
//	Y:  [64, 940]  → excursion 876
//	Cb: [64, 960]  → centered at 512, excursion 896
//	Cr: [64, 960]  → centered at 512, excursion 896
//
// The LUT must already be uploaded to GPU memory via UploadLUT3D.
// intensity controls blend: 0.0 = bypass, 1.0 = full grade.
func ColorGradeLUT3DP210(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}
	rc := C.launch_colorgrade_lut3d_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.float)(unsafe.Pointer(lutDevPtr)),
		C.int(lutSize),
		C.float(intensity),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: colorgrade LUT3D P210 failed: %d", rc)
	}
	return ctx.Sync()
}

// UploadLUT3D copies a flat float32 LUT array to GPU device memory.
// Returns the device pointer. Caller must call FreeLUT3D when done.
func UploadLUT3D(data []float32) (uintptr, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("gpu: empty LUT data")
	}

	var devPtr *C.float
	rc := C.colorgrade_upload_lut(
		&devPtr,
		(*C.float)(unsafe.Pointer(&data[0])),
		C.int(len(data)),
	)
	if rc != C.cudaSuccess {
		return 0, fmt.Errorf("gpu: upload LUT failed: %d", rc)
	}
	return uintptr(unsafe.Pointer(devPtr)), nil
}

// FreeLUT3D frees a LUT previously uploaded via UploadLUT3D.
func FreeLUT3D(devPtr uintptr) {
	if devPtr != 0 {
		C.colorgrade_free_lut((*C.float)(unsafe.Pointer(devPtr)))
	}
}
