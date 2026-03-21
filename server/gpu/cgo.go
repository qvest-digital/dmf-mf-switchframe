//go:build cgo && cuda

package gpu

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L${SRCDIR}/cuda -lswitchframe_cuda -L/usr/local/cuda/lib64 -lcuda -lcudart -lnppc -lnppi -lnppig -lnppidei

#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declarations for CUDA kernels (implemented in cuda/convert.cu)
cudaError_t yuv420p_to_nv12(
    uint8_t* nv12, const uint8_t* y, const uint8_t* cb, const uint8_t* cr,
    int width, int height, int nv12_pitch, int src_stride, cudaStream_t stream);
cudaError_t nv12_to_yuv420p(
    uint8_t* y, uint8_t* cb, uint8_t* cr, const uint8_t* nv12,
    int width, int height, int nv12_pitch, int dst_stride, cudaStream_t stream);
cudaError_t nv12_fill(
    uint8_t* nv12, int width, int height, int pitch,
    uint8_t yVal, uint8_t cbVal, uint8_t crVal, cudaStream_t stream);
*/
import "C"
