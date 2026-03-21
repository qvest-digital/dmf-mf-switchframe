//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// ============================================================================
// NVIDIA Optical Flow API types (from nvOpticalFlowCommon.h / nvOpticalFlowCuda.h)
// Minimal inline definitions to avoid header dependency. Loaded via dlopen.
// ============================================================================

typedef struct NvOFHandle_st*          NvOFHandle;
typedef struct NvOFGPUBufferHandle_st* NvOFGPUBufferHandle;

typedef int NV_OF_STATUS;
#define NV_OF_SUCCESS 0
#define NV_OF_API_VERSION 0x0500  // SDK 5.0 (major=5, minor=0)

// Enums (matching nvOpticalFlowCommon.h values)
#define NV_OF_FALSE 0
#define NV_OF_TRUE  1
#define NV_OF_MODE_OPTICALFLOW  1
#define NV_OF_PERF_LEVEL_SLOW   5
#define NV_OF_OUTPUT_VECTOR_GRID_SIZE_4 4
#define NV_OF_BUFFER_USAGE_INPUT  1
#define NV_OF_BUFFER_USAGE_OUTPUT 2
#define NV_OF_BUFFER_FORMAT_NV12   2
#define NV_OF_BUFFER_FORMAT_SHORT2 5
#define NV_OF_PRED_DIRECTION_FORWARD 0

// CUDA buffer type
#define NV_OF_CUDA_BUFFER_TYPE_CUDEVICEPTR 2

typedef struct {
    uint32_t width;
    uint32_t height;
    int      outGridSize;
    int      hintGridSize;
    int      mode;
    int      perfLevel;
    int      enableExternalHints;
    int      enableOutputCost;
    void*    hPrivData;
    int      disparityRange;
    int      enableRoi;
    int      predDirection;
    int      enableGlobalFlow;
    int      inputBufferFormat;
} NvOF_InitParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    int      bufferUsage;
    int      bufferFormat;
} NvOF_BufferDesc;

typedef struct {
    NvOFGPUBufferHandle inputFrame;
    NvOFGPUBufferHandle referenceFrame;
    NvOFGPUBufferHandle externalHints;
    int                 disableTemporalHints;
    uint32_t            padding;
    void*               hPrivData;
    uint32_t            padding2;
    uint32_t            numRois;
    void*               roiData;
} NvOF_ExecInParams;

typedef struct {
    NvOFGPUBufferHandle outputBuffer;
    NvOFGPUBufferHandle outputCostBuffer;
    void*               hPrivData;
    NvOFGPUBufferHandle bwdOutputBuffer;
    NvOFGPUBufferHandle bwdOutputCostBuffer;
    NvOFGPUBufferHandle globalFlowBuffer;
} NvOF_ExecOutParams;

typedef struct {
    uint32_t strideXInBytes;
    uint32_t strideYInBytes;
} NvOF_BufferStride;

typedef struct {
    NvOF_BufferStride strideInfo[3];
    uint32_t          numPlanes;
} NvOF_CudaBufferStrideInfo;

// Function pointer types (NVOFAPI = __stdcall on Windows, nothing on Linux)
typedef NV_OF_STATUS (*PFN_nvCreateOpticalFlowCuda)(CUcontext device, NvOFHandle* hOf);
typedef NV_OF_STATUS (*PFN_nvOFInit)(NvOFHandle hOf, const NvOF_InitParams* initParams);
typedef NV_OF_STATUS (*PFN_nvOFCreateGPUBufferCuda)(NvOFHandle hOf, const NvOF_BufferDesc* desc, int bufferType, NvOFGPUBufferHandle* hBuf);
typedef CUarray      (*PFN_nvOFGPUBufferGetCUarray)(NvOFGPUBufferHandle hBuf);
typedef CUdeviceptr  (*PFN_nvOFGPUBufferGetCUdeviceptr)(NvOFGPUBufferHandle hBuf);
typedef NV_OF_STATUS (*PFN_nvOFGPUBufferGetStrideInfo)(NvOFGPUBufferHandle hBuf, NvOF_CudaBufferStrideInfo* strideInfo);
typedef NV_OF_STATUS (*PFN_nvOFSetIOCudaStreams)(NvOFHandle hOf, CUstream inputStream, CUstream outputStream);
typedef NV_OF_STATUS (*PFN_nvOFExecute)(NvOFHandle hOf, const NvOF_ExecInParams* inParams, NvOF_ExecOutParams* outParams);
typedef NV_OF_STATUS (*PFN_nvOFDestroyGPUBufferCuda)(NvOFGPUBufferHandle hBuf);
typedef NV_OF_STATUS (*PFN_nvOFDestroy)(NvOFHandle hOf);
typedef NV_OF_STATUS (*PFN_nvOFGetLastError)(NvOFHandle hOf, char lastError[], uint32_t* size);
typedef NV_OF_STATUS (*PFN_nvOFGetCaps)(NvOFHandle hOf, int capsParam, uint32_t* capsVal, uint32_t* size);

// The function table populated by NvOFAPICreateInstanceCuda
typedef struct {
    PFN_nvCreateOpticalFlowCuda      nvCreateOpticalFlowCuda;
    PFN_nvOFInit                     nvOFInit;
    PFN_nvOFCreateGPUBufferCuda      nvOFCreateGPUBufferCuda;
    PFN_nvOFGPUBufferGetCUarray      nvOFGPUBufferGetCUarray;
    PFN_nvOFGPUBufferGetCUdeviceptr  nvOFGPUBufferGetCUdeviceptr;
    PFN_nvOFGPUBufferGetStrideInfo   nvOFGPUBufferGetStrideInfo;
    PFN_nvOFSetIOCudaStreams         nvOFSetIOCudaStreams;
    PFN_nvOFExecute                  nvOFExecute;
    PFN_nvOFDestroyGPUBufferCuda     nvOFDestroyGPUBufferCuda;
    PFN_nvOFDestroy                  nvOFDestroy;
    PFN_nvOFGetLastError             nvOFGetLastError;
    PFN_nvOFGetCaps                  nvOFGetCaps;
} NvOF_CudaFuncList;

typedef NV_OF_STATUS (*PFN_NvOFAPICreateInstanceCuda)(uint32_t apiVer, NvOF_CudaFuncList* funcList);

// Dynamic library state
static void* nvof_lib = NULL;
static NvOF_CudaFuncList nvof_funcs;
static int nvof_funcs_loaded = 0;

static int nvof_load(void) {
    if (nvof_lib) return 0;
    nvof_lib = dlopen("libnvidia-opticalflow.so.1", RTLD_LAZY);
    if (!nvof_lib) return -1;

    PFN_NvOFAPICreateInstanceCuda createInstance =
        (PFN_NvOFAPICreateInstanceCuda)dlsym(nvof_lib, "NvOFAPICreateInstanceCuda");
    if (!createInstance) {
        dlclose(nvof_lib);
        nvof_lib = NULL;
        return -2;
    }

    memset(&nvof_funcs, 0, sizeof(nvof_funcs));
    NV_OF_STATUS status = createInstance(NV_OF_API_VERSION, &nvof_funcs);
    if (status != NV_OF_SUCCESS) {
        dlclose(nvof_lib);
        nvof_lib = NULL;
        return -3;
    }

    nvof_funcs_loaded = 1;
    return 0;
}

static int nvof_available(void) {
    return nvof_funcs_loaded;
}

// ============================================================================
// NVOFA session management (C helpers called from Go)
// ============================================================================

typedef struct {
    NvOFHandle          hOf;
    NvOFGPUBufferHandle hInput;
    NvOFGPUBufferHandle hRef;
    NvOFGPUBufferHandle hOutput;
    CUdeviceptr         inputPtr;
    CUdeviceptr         refPtr;
    CUdeviceptr         outputPtr;
    uint32_t            outputStrideX;
    uint32_t            outputStrideY;
    int                 width;
    int                 height;
    int                 flowW;  // output grid: width/4
    int                 flowH;  // output grid: height/4
} NvOF_Session;

static NV_OF_STATUS nvof_create_session(NvOF_Session* s, int width, int height) {
    NV_OF_STATUS rc;
    memset(s, 0, sizeof(NvOF_Session));
    s->width = width;
    s->height = height;
    s->flowW = (width + 3) / 4;
    s->flowH = (height + 3) / 4;

    // Create OF handle (uses CUDA primary context)
    CUcontext cuCtx;
    cuCtxGetCurrent(&cuCtx);
    rc = nvof_funcs.nvCreateOpticalFlowCuda(cuCtx, &s->hOf);
    if (rc != NV_OF_SUCCESS) return rc;

    // Initialize: NV12 input, 4x4 grid, SLOW quality, forward prediction
    NvOF_InitParams init;
    memset(&init, 0, sizeof(init));
    init.width = width;
    init.height = height;
    init.outGridSize = NV_OF_OUTPUT_VECTOR_GRID_SIZE_4;
    init.mode = NV_OF_MODE_OPTICALFLOW;
    init.perfLevel = NV_OF_PERF_LEVEL_SLOW;
    init.predDirection = NV_OF_PRED_DIRECTION_FORWARD;
    init.inputBufferFormat = NV_OF_BUFFER_FORMAT_NV12;
    rc = nvof_funcs.nvOFInit(s->hOf, &init);
    if (rc != NV_OF_SUCCESS) { nvof_funcs.nvOFDestroy(s->hOf); return rc; }

    // Create input buffer
    NvOF_BufferDesc inputDesc = {width, height, NV_OF_BUFFER_USAGE_INPUT, NV_OF_BUFFER_FORMAT_NV12};
    rc = nvof_funcs.nvOFCreateGPUBufferCuda(s->hOf, &inputDesc, NV_OF_CUDA_BUFFER_TYPE_CUDEVICEPTR, &s->hInput);
    if (rc != NV_OF_SUCCESS) { nvof_destroy_session(s); return rc; }

    // Create reference buffer
    rc = nvof_funcs.nvOFCreateGPUBufferCuda(s->hOf, &inputDesc, NV_OF_CUDA_BUFFER_TYPE_CUDEVICEPTR, &s->hRef);
    if (rc != NV_OF_SUCCESS) { nvof_destroy_session(s); return rc; }

    // Create output buffer (SHORT2 = flow vectors)
    NvOF_BufferDesc outputDesc = {s->flowW, s->flowH, NV_OF_BUFFER_USAGE_OUTPUT, NV_OF_BUFFER_FORMAT_SHORT2};
    rc = nvof_funcs.nvOFCreateGPUBufferCuda(s->hOf, &outputDesc, NV_OF_CUDA_BUFFER_TYPE_CUDEVICEPTR, &s->hOutput);
    if (rc != NV_OF_SUCCESS) { nvof_destroy_session(s); return rc; }

    // Get device pointers for memcpy
    s->inputPtr = nvof_funcs.nvOFGPUBufferGetCUdeviceptr(s->hInput);
    s->refPtr = nvof_funcs.nvOFGPUBufferGetCUdeviceptr(s->hRef);
    s->outputPtr = nvof_funcs.nvOFGPUBufferGetCUdeviceptr(s->hOutput);

    // Get output stride
    NvOF_CudaBufferStrideInfo strideInfo;
    nvof_funcs.nvOFGPUBufferGetStrideInfo(s->hOutput, &strideInfo);
    s->outputStrideX = strideInfo.strideInfo[0].strideXInBytes;
    s->outputStrideY = strideInfo.strideInfo[0].strideYInBytes;

    return NV_OF_SUCCESS;
}

// nvof_get_input_stride retrieves the stride of an OF input buffer.
static uint32_t nvof_get_input_stride(NvOFGPUBufferHandle hBuf) {
    NvOF_CudaBufferStrideInfo si;
    memset(&si, 0, sizeof(si));
    nvof_funcs.nvOFGPUBufferGetStrideInfo(hBuf, &si);
    return si.strideInfo[0].strideXInBytes;
}

static NV_OF_STATUS nvof_compute_flow(NvOF_Session* s,
    CUdeviceptr prevNV12, int prevPitch,
    CUdeviceptr currNV12, int currPitch)
{
    // Copy input frames to OF buffers using 2D pitched copy.
    // Source frames use frame pool pitch; OF buffers have their own stride.
    uint32_t ofInputStride = nvof_get_input_stride(s->hInput);
    int h = s->height;
    int w = s->width;
    int totalRows = h + h / 2; // NV12: Y rows + UV rows

    CUDA_MEMCPY2D cp;
    memset(&cp, 0, sizeof(cp));
    cp.srcMemoryType = CU_MEMORYTYPE_DEVICE;
    cp.dstMemoryType = CU_MEMORYTYPE_DEVICE;
    cp.WidthInBytes = w;
    cp.Height = totalRows;

    // Copy prev frame (pitched source → OF buffer stride)
    cp.srcDevice = prevNV12;
    cp.srcPitch = prevPitch;
    cp.dstDevice = s->inputPtr;
    cp.dstPitch = ofInputStride;
    cuMemcpy2D(&cp);

    // Copy curr frame
    cp.srcDevice = currNV12;
    cp.srcPitch = currPitch;
    cp.dstDevice = s->refPtr;
    cp.dstPitch = nvof_get_input_stride(s->hRef);
    cuMemcpy2D(&cp);

    // Execute optical flow on NVOFA hardware
    NvOF_ExecInParams inParams;
    NvOF_ExecOutParams outParams;
    memset(&inParams, 0, sizeof(inParams));
    memset(&outParams, 0, sizeof(outParams));
    inParams.inputFrame = s->hInput;
    inParams.referenceFrame = s->hRef;
    outParams.outputBuffer = s->hOutput;

    return nvof_funcs.nvOFExecute(s->hOf, &inParams, &outParams);
}

static void nvof_destroy_session(NvOF_Session* s) {
    if (!s || !s->hOf) return;
    if (s->hOutput) nvof_funcs.nvOFDestroyGPUBufferCuda(s->hOutput);
    if (s->hRef) nvof_funcs.nvOFDestroyGPUBufferCuda(s->hRef);
    if (s->hInput) nvof_funcs.nvOFDestroyGPUBufferCuda(s->hInput);
    nvof_funcs.nvOFDestroy(s->hOf);
    memset(s, 0, sizeof(NvOF_Session));
}

// CUDA FRUC interpolation kernel (from fruc.cu)
cudaError_t fruc_interpolate_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    const int16_t* flowXY, int flowStride,
    int width, int height, float alpha,
    cudaStream_t stream);

cudaError_t fruc_blend_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    int width, int height, float alpha,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"unsafe"
)

// FRUC provides GPU-accelerated frame rate up-conversion.
//
// When the NVIDIA Optical Flow engine (NVOFA) is available, it computes
// hardware-accelerated motion vectors on dedicated silicon (independent of
// CUDA cores), then uses a custom CUDA kernel for motion-compensated
// interpolation. When NVOFA is unavailable, falls back to linear blend.
type FRUC struct {
	gpuCtx   *Context
	width    int
	height   int
	hasNVOFA bool

	// NVOFA session (only when hasNVOFA=true)
	session C.NvOF_Session

	// Flow vector buffer in VRAM (int16_t pairs at 4x4 block granularity)
	// When NVOFA is active, flow vectors are copied here from the OF output buffer.
	// When NVOFA is inactive, this is zero-filled (blend fallback).
	flowBuf unsafe.Pointer
	flowW   int // flow field width (frame width / 4)
	flowH   int // flow field height (frame height / 4)
}

var (
	nvofLoadOnce sync.Once
	nvofLoaded   bool
)

// FRUCAvailable returns true if the GPU FRUC subsystem can be used.
// Always true on CUDA builds (blend fallback), but NVOFA hardware
// acceleration depends on libnvidia-opticalflow.so availability.
func FRUCAvailable() bool {
	return true // CUDA blend fallback always works
}

// nvofaAvailable checks if the NVOFA hardware optical flow library is present.
func nvofaAvailable() bool {
	nvofLoadOnce.Do(func() {
		rc := C.nvof_load()
		nvofLoaded = (rc == 0)
		if nvofLoaded {
			slog.Info("gpu: NVOFA optical flow library loaded (function table populated)")
		} else {
			slog.Debug("gpu: NVOFA library not available (using blend fallback)", "rc", rc)
		}
	})
	return nvofLoaded
}

// NewFRUC creates a FRUC instance for the given frame dimensions.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}

	f := &FRUC{
		gpuCtx: ctx,
		width:  width,
		height: height,
		flowW:  (width + 3) / 4,
		flowH:  (height + 3) / 4,
	}

	// Allocate flow vector buffer: int16_t pairs (dx, dy) per 4x4 block
	flowSize := f.flowW * f.flowH * 2 * 2 // 2 components * 2 bytes each
	if rc := C.cudaMalloc(&f.flowBuf, C.size_t(flowSize)); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: FRUC flow buffer alloc failed: %d", rc)
	}
	C.cudaMemset(f.flowBuf, 0, C.size_t(flowSize))

	// Try to create NVOFA session
	if nvofaAvailable() {
		rc := C.nvof_create_session(&f.session, C.int(width), C.int(height))
		if rc == C.NV_OF_SUCCESS {
			f.hasNVOFA = true
			slog.Info("gpu: FRUC initialized with NVOFA hardware optical flow",
				"width", width, "height", height,
				"flow_grid", fmt.Sprintf("%dx%d", f.flowW, f.flowH))
		} else {
			slog.Warn("gpu: NVOFA session creation failed, using blend fallback",
				"rc", rc, "width", width, "height", height)
		}
	}

	if !f.hasNVOFA {
		slog.Info("gpu: FRUC initialized with blend fallback",
			"width", width, "height", height)
	}

	return f, nil
}

// Interpolate generates an intermediate frame between prev and curr.
// alpha is the temporal position: 0.0 = prev, 1.0 = curr.
//
// When NVOFA is active: computes hardware optical flow → motion-compensated warp.
// When NVOFA is inactive: GPU-accelerated linear blend.
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame, alpha float64) error {
	if f == nil || prev == nil || curr == nil || output == nil {
		return ErrGPUNotAvailable
	}

	if f.hasNVOFA {
		// Compute optical flow on NVOFA hardware engine
		rc := C.nvof_compute_flow(&f.session,
			C.CUdeviceptr(prev.DevPtr), C.int(prev.Pitch),
			C.CUdeviceptr(curr.DevPtr), C.int(curr.Pitch),
		)
		if rc == C.NV_OF_SUCCESS {
			// Copy flow vectors from OF output buffer to our flow buffer,
			// respecting the OF output stride (hardware buffers are often
			// aligned to power-of-2 boundaries).
			dstStride := C.size_t(f.flowW * 4) // our buffer: flowW * sizeof(int16_t) * 2
			srcStride := C.size_t(f.session.outputStrideX)
			if srcStride == dstStride {
				// Strides match — flat copy
				flowBytes := C.size_t(f.flowW * f.flowH * 4)
				C.cuMemcpy(C.CUdeviceptr(uintptr(f.flowBuf)),
					f.session.outputPtr, flowBytes)
			} else {
				// Strides differ — row-by-row 2D copy
				var cp C.CUDA_MEMCPY2D
				C.memset(unsafe.Pointer(&cp), 0, C.size_t(unsafe.Sizeof(cp)))
				cp.srcMemoryType = C.CU_MEMORYTYPE_DEVICE
				cp.dstMemoryType = C.CU_MEMORYTYPE_DEVICE
				cp.srcDevice = f.session.outputPtr
				cp.srcPitch = srcStride
				cp.dstDevice = C.CUdeviceptr(uintptr(f.flowBuf))
				cp.dstPitch = dstStride
				cp.WidthInBytes = dstStride
				cp.Height = C.size_t(f.flowH)
				C.cuMemcpy2D(&cp)
			}

			// Motion-compensated interpolation with NVOFA S10.5 flow vectors.
			// The CUDA kernel converts S10.5 to pixels via * 0.03125f (1/32).
			cerr := C.fruc_interpolate_nv12(
				(*C.uint8_t)(unsafe.Pointer(uintptr(output.DevPtr))),
				C.int(output.Pitch),
				(*C.uint8_t)(unsafe.Pointer(uintptr(prev.DevPtr))),
				C.int(prev.Pitch),
				(*C.uint8_t)(unsafe.Pointer(uintptr(curr.DevPtr))),
				C.int(curr.Pitch),
				(*C.int16_t)(f.flowBuf),
				C.int(f.flowW*2),
				C.int(f.width), C.int(f.height),
				C.float(alpha),
				f.gpuCtx.stream,
			)
			if cerr != C.cudaSuccess {
				return fmt.Errorf("gpu: FRUC interpolate failed: %d", cerr)
			}
			return f.gpuCtx.Sync()
		}

		// OF execution failed — fall through to blend
		slog.Debug("gpu: NVOFA execute failed, falling back to blend", "rc", rc)
	}

	// Fallback: linear blend (still GPU-accelerated)
	rc := C.fruc_blend_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(output.DevPtr))),
		C.int(output.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(prev.DevPtr))),
		C.int(prev.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(curr.DevPtr))),
		C.int(curr.Pitch),
		C.int(f.width), C.int(f.height),
		C.float(alpha),
		f.gpuCtx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: FRUC blend fallback failed: %d", rc)
	}
	return f.gpuCtx.Sync()
}

// Close releases FRUC resources.
func (f *FRUC) Close() {
	if f == nil {
		return
	}
	if f.hasNVOFA {
		C.nvof_destroy_session(&f.session)
		f.hasNVOFA = false
	}
	if f.flowBuf != nil {
		C.cudaFree(f.flowBuf)
		f.flowBuf = nil
	}
}
