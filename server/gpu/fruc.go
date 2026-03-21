//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// NvOFFRUC types (from NvOFFRUC.h, simplified for cgo compatibility)
// We use dlopen/dlsym to load the library at runtime since it may not be present.

typedef struct _NvOFFRUC* NvOFFRUCHandle;

typedef enum {
    NvOFFRUC_SUCCESS = 0,
    NvOFFRUC_ERR_NOT_SUPPORTED = 1,
    NvOFFRUC_ERR_GENERIC = 16,
} NvOFFRUC_STATUS_ENUM;

typedef int NvOFFRUC_STATUS;

typedef enum { CudaResourceCuDevicePtr = 0 } NvOFFRUCCUDAResourceType_e;
typedef enum { CudaResource_e = 0 } NvOFFRUCResourceType_e;
typedef enum { NV12Surface_e = 0 } NvOFFRUCSurfaceFormat_e;

typedef struct {
    uint32_t    uiWidth;
    uint32_t    uiHeight;
    void*       pDevice;
    int         eResourceType;      // NvOFFRUCResourceType
    int         eSurfaceFormat;     // NvOFFRUCSurfaceFormat
    int         eCUDAResourceType;  // NvOFFRUCCUDAResourceType
    uint32_t    uiReserved[32];
} NvOFFRUC_CREATE_PARAM_C;

typedef struct {
    void*       pFrame;
    double      nTimeStamp;
    size_t      nCuSurfacePitch;
    int*        bHasFrameRepetitionOccurred;  // bool* in original
    uint32_t    uiReserved[32];
} NvOFFRUC_FRAMEDATA_C;

typedef struct {
    uint64_t pad[2];
} SyncWait_C;

typedef struct {
    uint64_t pad[2];
} SyncSignal_C;

typedef struct {
    NvOFFRUC_FRAMEDATA_C  stFrameDataInput;
    uint32_t              bSkipWarp;
    SyncWait_C            uSyncWait;
    uint32_t              uiReserved[32];
} NvOFFRUC_PROCESS_IN_PARAMS_C;

typedef struct {
    NvOFFRUC_FRAMEDATA_C  stFrameDataOutput;
    SyncSignal_C          uSyncSignal;
    uint32_t              uiReserved[32];
} NvOFFRUC_PROCESS_OUT_PARAMS_C;

#define FRUC_MAX_RESOURCE 10

typedef struct {
    void*       pArrResource[FRUC_MAX_RESOURCE];
    void*       pD3D11FenceObj;
    uint32_t    uiCount;
} NvOFFRUC_REGISTER_RESOURCE_PARAM_C;

typedef struct {
    void*       pArrResource[FRUC_MAX_RESOURCE];
    uint32_t    uiCount;
} NvOFFRUC_UNREGISTER_RESOURCE_PARAM_C;

// Function pointer types
typedef NvOFFRUC_STATUS (*PFN_NvOFFRUCCreate)(const NvOFFRUC_CREATE_PARAM_C*, NvOFFRUCHandle*);
typedef NvOFFRUC_STATUS (*PFN_NvOFFRUCRegisterResource)(NvOFFRUCHandle, const NvOFFRUC_REGISTER_RESOURCE_PARAM_C*);
typedef NvOFFRUC_STATUS (*PFN_NvOFFRUCUnregisterResource)(NvOFFRUCHandle, const NvOFFRUC_UNREGISTER_RESOURCE_PARAM_C*);
typedef NvOFFRUC_STATUS (*PFN_NvOFFRUCProcess)(NvOFFRUCHandle, const NvOFFRUC_PROCESS_IN_PARAMS_C*, const NvOFFRUC_PROCESS_OUT_PARAMS_C*);
typedef NvOFFRUC_STATUS (*PFN_NvOFFRUCDestroy)(NvOFFRUCHandle);

// Dynamic library state
static void* fruc_lib = NULL;
static PFN_NvOFFRUCCreate       fn_create = NULL;
static PFN_NvOFFRUCRegisterResource fn_register = NULL;
static PFN_NvOFFRUCUnregisterResource fn_unregister = NULL;
static PFN_NvOFFRUCProcess      fn_process = NULL;
static PFN_NvOFFRUCDestroy      fn_destroy = NULL;

static int fruc_load_library(void) {
    if (fruc_lib) return 0; // already loaded

    fruc_lib = dlopen("libNvOFFRUC.so", RTLD_LAZY);
    if (!fruc_lib) return -1;

    fn_create     = (PFN_NvOFFRUCCreate)dlsym(fruc_lib, "NvOFFRUCCreate");
    fn_register   = (PFN_NvOFFRUCRegisterResource)dlsym(fruc_lib, "NvOFFRUCRegisterResource");
    fn_unregister = (PFN_NvOFFRUCUnregisterResource)dlsym(fruc_lib, "NvOFFRUCUnregisterResource");
    fn_process    = (PFN_NvOFFRUCProcess)dlsym(fruc_lib, "NvOFFRUCProcess");
    fn_destroy    = (PFN_NvOFFRUCDestroy)dlsym(fruc_lib, "NvOFFRUCDestroy");

    if (!fn_create || !fn_register || !fn_process || !fn_destroy) {
        dlclose(fruc_lib);
        fruc_lib = NULL;
        return -2;
    }
    return 0;
}

static int fruc_available(void) {
    return fruc_lib != NULL && fn_create != NULL;
}

static NvOFFRUC_STATUS fruc_create(NvOFFRUCHandle* handle, int width, int height) {
    NvOFFRUC_CREATE_PARAM_C params;
    memset(&params, 0, sizeof(params));
    params.uiWidth = width;
    params.uiHeight = height;
    params.pDevice = NULL; // CUDA uses current context
    params.eResourceType = 0;      // CudaResource
    params.eSurfaceFormat = 0;     // NV12Surface
    params.eCUDAResourceType = 0;  // CudaResourceCuDevicePtr
    return fn_create(&params, handle);
}

static NvOFFRUC_STATUS fruc_register_resources(NvOFFRUCHandle handle,
    void* res0, void* res1, void* res2) {
    NvOFFRUC_REGISTER_RESOURCE_PARAM_C params;
    memset(&params, 0, sizeof(params));
    params.pArrResource[0] = res0;
    params.pArrResource[1] = res1;
    params.pArrResource[2] = res2;
    params.uiCount = 3;
    return fn_register(handle, &params);
}

static NvOFFRUC_STATUS fruc_process(NvOFFRUCHandle handle,
    void* inputFrame, double inputTS, size_t inputPitch,
    void* outputFrame, double outputTS, size_t outputPitch,
    int skipWarp, int* frameRepeated) {
    NvOFFRUC_PROCESS_IN_PARAMS_C inParams;
    NvOFFRUC_PROCESS_OUT_PARAMS_C outParams;
    memset(&inParams, 0, sizeof(inParams));
    memset(&outParams, 0, sizeof(outParams));

    inParams.stFrameDataInput.pFrame = inputFrame;
    inParams.stFrameDataInput.nTimeStamp = inputTS;
    inParams.stFrameDataInput.nCuSurfacePitch = inputPitch;
    inParams.stFrameDataInput.bHasFrameRepetitionOccurred = frameRepeated;
    inParams.bSkipWarp = skipWarp ? 1 : 0;

    outParams.stFrameDataOutput.pFrame = outputFrame;
    outParams.stFrameDataOutput.nTimeStamp = outputTS;
    outParams.stFrameDataOutput.nCuSurfacePitch = outputPitch;

    return fn_process(handle, &inParams, &outParams);
}

static NvOFFRUC_STATUS fruc_destroy(NvOFFRUCHandle handle) {
    if (fn_destroy) return fn_destroy(handle);
    return -1;
}
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"unsafe"
)

// FRUC provides GPU-accelerated frame rate up-conversion using NVIDIA's
// hardware Optical Flow Accelerator (NVOFA) via the NvOFFRUC library.
// The NVOFA engine runs on dedicated silicon, independent of CUDA cores.
type FRUC struct {
	handle   C.NvOFFRUCHandle
	gpuCtx   *Context
	width    int
	height   int
	pitch    int
	frameIdx int64

	// Resource pool: 3 registered CUdeviceptr buffers (NvOFFRUC minimum)
	resources [3]*GPUFrame
	pool      *FramePool
}

var (
	frucLoadOnce sync.Once
	frucLoaded   bool
)

// FRUCAvailable returns true if the NvOFFRUC library is available.
func FRUCAvailable() bool {
	frucLoadOnce.Do(func() {
		rc := C.fruc_load_library()
		frucLoaded = (rc == 0)
		if frucLoaded {
			slog.Info("gpu: NvOFFRUC library loaded")
		} else {
			slog.Debug("gpu: NvOFFRUC library not available", "rc", rc)
		}
	})
	return frucLoaded
}

// NewFRUC creates a FRUC instance for the given frame dimensions.
// Returns ErrGPUNotAvailable if the NvOFFRUC library is not present.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	if !FRUCAvailable() {
		return nil, fmt.Errorf("gpu: NvOFFRUC library not available")
	}

	f := &FRUC{
		gpuCtx: ctx,
		width:  width,
		height: height,
	}

	// Create frame pool for FRUC resources
	pool, err := NewFramePool(ctx, width, height, 3)
	if err != nil {
		return nil, fmt.Errorf("gpu: FRUC pool creation failed: %w", err)
	}
	f.pool = pool
	f.pitch = pool.Pitch()

	// Pre-allocate 3 resource frames (NvOFFRUC minimum)
	for i := 0; i < 3; i++ {
		frame, err := pool.Acquire()
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("gpu: FRUC resource alloc %d failed: %w", i, err)
		}
		f.resources[i] = frame
	}

	// Create NvOFFRUC handle
	rc := C.fruc_create(&f.handle, C.int(width), C.int(height))
	if rc != 0 {
		f.Close()
		return nil, fmt.Errorf("gpu: NvOFFRUCCreate failed: %d", rc)
	}

	// Register resource buffers
	rc = C.fruc_register_resources(f.handle,
		unsafe.Pointer(uintptr(f.resources[0].DevPtr)),
		unsafe.Pointer(uintptr(f.resources[1].DevPtr)),
		unsafe.Pointer(uintptr(f.resources[2].DevPtr)),
	)
	if rc != 0 {
		f.Close()
		return nil, fmt.Errorf("gpu: NvOFFRUCRegisterResource failed: %d", rc)
	}

	slog.Info("gpu: FRUC initialized", "width", width, "height", height)
	return f, nil
}

// Interpolate generates an intermediate frame between prev and curr at the
// specified temporal position (0.0 = prev, 1.0 = curr). The result is written
// to the output GPUFrame. Uses the NVOFA hardware engine for optical flow.
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame) error {
	if f == nil || f.handle == nil {
		return ErrGPUNotAvailable
	}

	// Feed prev frame (skipWarp=1 for first frame of pair)
	prevTS := float64(f.frameIdx)
	f.frameIdx++
	var repeated C.int
	rc := C.fruc_process(f.handle,
		unsafe.Pointer(uintptr(prev.DevPtr)), C.double(prevTS), C.size_t(prev.Pitch),
		unsafe.Pointer(uintptr(output.DevPtr)), C.double(prevTS+0.5), C.size_t(output.Pitch),
		C.int(1), &repeated, // skipWarp=1: just register this frame
	)
	if rc != 0 {
		return fmt.Errorf("gpu: FRUC process prev failed: %d", rc)
	}

	// Feed curr frame (skipWarp=0 to trigger interpolation)
	currTS := float64(f.frameIdx)
	f.frameIdx++
	rc = C.fruc_process(f.handle,
		unsafe.Pointer(uintptr(curr.DevPtr)), C.double(currTS), C.size_t(curr.Pitch),
		unsafe.Pointer(uintptr(output.DevPtr)), C.double(prevTS+0.5), C.size_t(output.Pitch),
		C.int(0), &repeated, // skipWarp=0: interpolate between prev and curr
	)
	if rc != 0 {
		return fmt.Errorf("gpu: FRUC process curr failed: %d", rc)
	}

	return nil
}

// Close releases FRUC resources.
func (f *FRUC) Close() {
	if f == nil {
		return
	}
	if f.handle != nil {
		C.fruc_destroy(f.handle)
		f.handle = nil
	}
	for i, res := range f.resources {
		if res != nil {
			res.Release()
			f.resources[i] = nil
		}
	}
	if f.pool != nil {
		f.pool.Close()
		f.pool = nil
	}
}
