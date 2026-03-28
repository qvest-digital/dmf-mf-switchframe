//go:build cgo && cuda

package sidecar

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import "fmt"

// InitCUDADriver initializes the CUDA driver and runtime APIs.
// Calls cuInit(0) + cudaSetDevice(0) to create a device context.
// Required before ImportIPCHandle can be used in the host process.
// Safe to call multiple times (idempotent).
func InitCUDADriver() error {
	if rc := C.cuInit(0); rc != C.CUDA_SUCCESS {
		return fmt.Errorf("cuInit: error %d", rc)
	}
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return fmt.Errorf("cudaSetDevice: error %d", rc)
	}
	return nil
}
