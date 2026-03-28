//go:build cgo && cuda && tensorrt

package main

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"
import "fmt"

// initCUDA initializes the CUDA driver and selects the given device.
func initCUDA(device int) error {
	if rc := C.cuInit(0); rc != C.CUDA_SUCCESS {
		return fmt.Errorf("cuInit: error %d", rc)
	}

	var count C.int
	if rc := C.cudaGetDeviceCount(&count); rc != C.cudaSuccess {
		return fmt.Errorf("cudaGetDeviceCount: error %d", rc)
	}
	if device < 0 || device >= int(count) {
		return fmt.Errorf("device %d out of range (have %d devices)", device, int(count))
	}

	if rc := C.cudaSetDevice(C.int(device)); rc != C.cudaSuccess {
		return fmt.Errorf("cudaSetDevice(%d): error %d", device, rc)
	}
	return nil
}
