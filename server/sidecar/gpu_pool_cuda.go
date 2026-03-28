//go:build cgo && cuda

package sidecar

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

// shm_open is in libc on macOS, but needs -lrt on Linux.
#cgo linux LDFLAGS: -lrt

static int gpu_shm_create(const char *name) {
	return shm_open(name, O_RDWR | O_CREAT | O_EXCL, 0600);
}

static int gpu_shm_open(const char *name) {
	return shm_open(name, O_RDWR, 0);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// CUDAIPCHandle is a serializable handle to a CUDA device allocation.
// Exactly 64 bytes (CU_IPC_HANDLE_SIZE), can be sent over a socket to another
// process on the same machine for zero-copy GPU memory sharing.
type CUDAIPCHandle [64]byte

// CUDAIPCEventHandle is a serializable handle to a CUDA IPC event.
// Exactly 64 bytes, used for cross-process GPU stream synchronization.
type CUDAIPCEventHandle [64]byte

// CUDAGPUPool implements a shared VRAM pool using CUDA IPC.
//
// The host process allocates NV12 frames and result buffers in GPU VRAM,
// exports IPC handles via cuIpcGetMemHandle, and sends them to the worker
// over the control socket. The worker imports the handles via cuIpcOpenMemHandle,
// gaining direct GPU-addressable pointers to the same physical VRAM.
//
// GPU synchronization uses CUDA IPC events:
//   - readyEvents: host records after writing a frame, worker waits before reading
//   - doneEvents: worker records after writing results, host waits before reading
//
// CPU-side coordination uses the GPUPoolControl shared memory region with
// atomic slot states (Idle -> Ready -> Processing -> Done -> Idle).
type CUDAGPUPool struct {
	cfg         GPUPoolConfig
	ctrl        *GPUPoolControl
	ctrlShm     []byte // mmap'd POSIX shm for control region
	ctrlShmName string

	// Host-side: device pointers + IPC handles for frame input slots.
	frameDevPtrs []C.CUdeviceptr
	frameHandles []CUDAIPCHandle

	// Result slots (also in VRAM), written by worker, read by host.
	resultDevPtrs []C.CUdeviceptr
	resultHandles []CUDAIPCHandle

	// GPU sync events (per-slot, for cross-process GPU stream ordering).
	readyEvents  []C.cudaEvent_t
	doneEvents   []C.cudaEvent_t
	readyHandles []CUDAIPCEventHandle
	doneHandles  []CUDAIPCEventHandle

	isHost    bool // true for host side, false for worker
	ownsFrames bool // true if we allocated frame VRAM (false for zero-copy export)
}

// NewCUDAGPUPoolHost allocates a shared VRAM pool for cross-process GPU frame exchange.
//
// It performs the following:
//  1. Validates the config
//  2. Allocates NV12 frame slots in VRAM via cudaMallocPitch
//  3. Allocates result slots in VRAM (if ResultSize > 0)
//  4. Exports IPC memory handles for each allocation
//  5. Creates IPC-capable CUDA events for GPU stream synchronization
//  6. Creates a POSIX shared memory segment for the control region
//
// The returned pool's handles can be sent to the worker via the control socket.
// shmName must start with '/' (POSIX shm requirement).
func NewCUDAGPUPoolHost(cfg GPUPoolConfig, shmName string) (*CUDAGPUPool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(shmName) == 0 || shmName[0] != '/' {
		return nil, fmt.Errorf("gpu pool: shmName must start with '/'")
	}

	p := &CUDAGPUPool{
		cfg:         cfg,
		ctrlShmName: shmName,
		isHost:      true,
		ownsFrames:  true,
	}

	// Allocate frame slots.
	if err := p.allocFrameSlots(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: alloc frame slots: %w", err)
	}

	// Export IPC handles for frame slots.
	if err := p.exportFrameHandles(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: export frame handles: %w", err)
	}

	// Allocate result slots (if configured).
	if cfg.ResultSize > 0 {
		if err := p.allocResultSlots(); err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu pool: alloc result slots: %w", err)
		}
		if err := p.exportResultHandles(); err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu pool: export result handles: %w", err)
		}
	}

	// Create IPC events for GPU synchronization.
	if err := p.createEvents(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: create events: %w", err)
	}

	// Export event handles.
	if err := p.exportEventHandles(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: export event handles: %w", err)
	}

	// Create POSIX shared memory for the control region.
	if err := p.createControlShm(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: create control shm: %w", err)
	}

	return p, nil
}

// NewCUDAGPUPoolHostFromPtrs creates a shared VRAM pool that exports EXISTING
// device pointers as IPC handles, rather than allocating new VRAM. This enables
// zero-copy sharing of the pipeline's existing FramePool with the sidecar —
// the sidecar reads frames directly from the same physical VRAM the pipeline
// already writes to.
//
// framePtrs are the CUdeviceptr values from the pipeline's pre-allocated FramePool.
// The caller retains ownership — Close() will NOT free these pointers.
//
// Result slots are NOT allocated (use dynamic mask handle exchange instead).
func NewCUDAGPUPoolHostFromPtrs(framePtrs []uintptr, cfg GPUPoolConfig, shmName string) (*CUDAGPUPool, error) {
	if len(framePtrs) == 0 {
		return nil, fmt.Errorf("gpu pool: no frame pointers provided")
	}
	cfg.SlotCount = len(framePtrs)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(shmName) == 0 || shmName[0] != '/' {
		return nil, fmt.Errorf("gpu pool: shmName must start with '/'")
	}

	p := &CUDAGPUPool{
		cfg:         cfg,
		ctrlShmName: shmName,
		isHost:      true,
		ownsFrames:  false, // we do NOT own these allocations
	}

	// Store external device pointers.
	p.frameDevPtrs = make([]C.CUdeviceptr, len(framePtrs))
	for i, ptr := range framePtrs {
		p.frameDevPtrs[i] = C.CUdeviceptr(ptr)
	}

	// Export IPC handles for the external pointers.
	if err := p.exportFrameHandles(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: export frame handles: %w", err)
	}

	// Create IPC events.
	if err := p.createEvents(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: create events: %w", err)
	}
	if err := p.exportEventHandles(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: export event handles: %w", err)
	}

	// Create control region.
	if err := p.createControlShm(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: create control shm: %w", err)
	}

	return p, nil
}

// ExportIPCHandle exports a single device pointer as a CUDA IPC handle.
// Used for dynamic handle exchange (e.g., sidecar exporting mask buffers
// back to the host).
//
// Calls cudaSetDevice(0) first to ensure a CUDA context is active on the
// calling OS thread (Go goroutines migrate between threads).
func ExportIPCHandle(devPtr uintptr) (CUDAIPCHandle, error) {
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return CUDAIPCHandle{}, fmt.Errorf("cudaSetDevice before IPC export: error %d", rc)
	}

	var handle C.CUipcMemHandle
	rc := C.cuIpcGetMemHandle(&handle, C.CUdeviceptr(devPtr))
	if rc != C.CUDA_SUCCESS {
		return CUDAIPCHandle{}, fmt.Errorf("cuIpcGetMemHandle: error %d", rc)
	}
	var h CUDAIPCHandle
	copy(h[:], C.GoBytes(unsafe.Pointer(&handle), 64))
	return h, nil
}

// ImportIPCHandle imports a CUDA IPC handle and returns a local device pointer.
// The caller must call CloseIPCHandle when done.
//
// Calls cudaSetDevice(0) first to ensure a CUDA context is active on the
// calling OS thread. This is necessary because Go goroutines can migrate
// between OS threads, and cuIpcOpenMemHandle requires a valid CUDA context.
func ImportIPCHandle(h CUDAIPCHandle) (uintptr, error) {
	// Ensure CUDA context on this thread (goroutines migrate between OS threads).
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return 0, fmt.Errorf("cudaSetDevice before IPC import: error %d", rc)
	}

	var cHandle C.CUipcMemHandle
	C.memcpy(unsafe.Pointer(&cHandle), unsafe.Pointer(&h[0]), 64)
	var dptr C.CUdeviceptr
	rc := C.cuIpcOpenMemHandle(&dptr, cHandle, C.CU_IPC_MEM_LAZY_ENABLE_PEER_ACCESS)
	if rc != C.CUDA_SUCCESS {
		return 0, fmt.Errorf("cuIpcOpenMemHandle: error %d", rc)
	}
	return uintptr(dptr), nil
}

// CloseIPCHandle releases an imported IPC handle.
func CloseIPCHandle(devPtr uintptr) {
	C.cuIpcCloseMemHandle(C.CUdeviceptr(devPtr))
}

// OpenCUDAGPUPoolWorker opens an existing CUDA IPC GPU pool from the worker side.
//
// It imports the IPC memory handles exported by the host, opens the IPC events,
// and maps the shared memory control region. The worker gets GPU-addressable
// pointers to the same physical VRAM as the host.
func OpenCUDAGPUPoolWorker(
	frameHandles, resultHandles []CUDAIPCHandle,
	readyHandles, doneHandles []CUDAIPCEventHandle,
	cfg GPUPoolConfig,
	shmName string,
) (*CUDAGPUPool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(frameHandles) != cfg.SlotCount {
		return nil, fmt.Errorf("gpu pool: frame handle count %d != slot count %d", len(frameHandles), cfg.SlotCount)
	}
	if cfg.ResultSize > 0 && len(resultHandles) != cfg.SlotCount {
		return nil, fmt.Errorf("gpu pool: result handle count %d != slot count %d", len(resultHandles), cfg.SlotCount)
	}
	if len(readyHandles) != cfg.SlotCount || len(doneHandles) != cfg.SlotCount {
		return nil, fmt.Errorf("gpu pool: event handle count mismatch")
	}

	p := &CUDAGPUPool{
		cfg:           cfg,
		ctrlShmName:   shmName,
		isHost:        false,
		frameHandles:  make([]CUDAIPCHandle, cfg.SlotCount),
		resultHandles: make([]CUDAIPCHandle, len(resultHandles)),
		readyHandles:  make([]CUDAIPCEventHandle, cfg.SlotCount),
		doneHandles:   make([]CUDAIPCEventHandle, cfg.SlotCount),
	}
	copy(p.frameHandles, frameHandles)
	copy(p.resultHandles, resultHandles)
	copy(p.readyHandles, readyHandles)
	copy(p.doneHandles, doneHandles)

	// Import frame memory handles.
	if err := p.importFrameHandles(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: import frame handles: %w", err)
	}

	// Import result memory handles.
	if cfg.ResultSize > 0 {
		if err := p.importResultHandles(); err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu pool: import result handles: %w", err)
		}
	}

	// Open IPC events.
	if err := p.openEvents(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: open events: %w", err)
	}

	// Open the control region shared memory.
	if err := p.openControlShm(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: open control shm: %w", err)
	}

	// Validate the control region header.
	if err := p.ctrl.ValidateHeader(); err != nil {
		p.Close()
		return nil, fmt.Errorf("gpu pool: %w", err)
	}

	return p, nil
}

// --- Accessors ---

// FrameHandles returns the IPC handles for frame input slots.
func (p *CUDAGPUPool) FrameHandles() []CUDAIPCHandle {
	out := make([]CUDAIPCHandle, len(p.frameHandles))
	copy(out, p.frameHandles)
	return out
}

// ResultHandles returns the IPC handles for result output slots.
func (p *CUDAGPUPool) ResultHandles() []CUDAIPCHandle {
	out := make([]CUDAIPCHandle, len(p.resultHandles))
	copy(out, p.resultHandles)
	return out
}

// ReadyEventHandles returns the IPC event handles for ready signals.
func (p *CUDAGPUPool) ReadyEventHandles() []CUDAIPCEventHandle {
	out := make([]CUDAIPCEventHandle, len(p.readyHandles))
	copy(out, p.readyHandles)
	return out
}

// DoneEventHandles returns the IPC event handles for done signals.
func (p *CUDAGPUPool) DoneEventHandles() []CUDAIPCEventHandle {
	out := make([]CUDAIPCEventHandle, len(p.doneHandles))
	copy(out, p.doneHandles)
	return out
}

// FrameDevPtr returns the device pointer for the given frame slot.
// The pointer is valid in the calling process's CUDA address space.
func (p *CUDAGPUPool) FrameDevPtr(slot int) uintptr {
	if slot < 0 || slot >= len(p.frameDevPtrs) {
		return 0
	}
	return uintptr(p.frameDevPtrs[slot])
}

// ResultDevPtr returns the device pointer for the given result slot.
func (p *CUDAGPUPool) ResultDevPtr(slot int) uintptr {
	if slot < 0 || slot >= len(p.resultDevPtrs) {
		return 0
	}
	return uintptr(p.resultDevPtrs[slot])
}

// ReadyEvent returns the CUDA event for recording after writing a frame to a slot.
// The host records this event after uploading frame data; the worker waits on it
// before reading the frame. Returns nil for invalid slot indices.
func (p *CUDAGPUPool) ReadyEvent(slot int) C.cudaEvent_t {
	if slot < 0 || slot >= len(p.readyEvents) {
		return nil
	}
	return p.readyEvents[slot]
}

// DoneEvent returns the CUDA event for recording after writing results to a slot.
// The worker records this event after inference completes; the host waits on it
// before reading results. Returns nil for invalid slot indices.
func (p *CUDAGPUPool) DoneEvent(slot int) C.cudaEvent_t {
	if slot < 0 || slot >= len(p.doneEvents) {
		return nil
	}
	return p.doneEvents[slot]
}

// Control returns the shared control region.
func (p *CUDAGPUPool) Control() *GPUPoolControl {
	return p.ctrl
}

// Config returns the pool configuration.
func (p *CUDAGPUPool) Config() GPUPoolConfig {
	return p.cfg
}

// SlotCount returns the number of slots in the pool.
func (p *CUDAGPUPool) SlotCount() int {
	return p.cfg.SlotCount
}

// --- Internal allocation (host side) ---

// allocFrameSlots allocates NV12 frame buffers in VRAM using cudaMallocPitch.
func (p *CUDAGPUPool) allocFrameSlots() error {
	p.frameDevPtrs = make([]C.CUdeviceptr, p.cfg.SlotCount)

	// NV12 total height: height + height/2 (Y plane + interleaved UV plane).
	totalHeight := C.size_t(p.cfg.Height * 3 / 2)
	width := C.size_t(p.cfg.Width)

	for i := 0; i < p.cfg.SlotCount; i++ {
		var devPtr unsafe.Pointer
		var pitch C.size_t
		rc := C.cudaMallocPitch(&devPtr, &pitch, width, totalHeight)
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaMallocPitch slot %d: error %d", i, rc)
		}
		p.frameDevPtrs[i] = C.CUdeviceptr(uintptr(devPtr))

		// Validate pitch matches config (all slots must have consistent pitch).
		if i == 0 && p.cfg.Pitch == 0 {
			p.cfg.Pitch = int(pitch)
		} else if int(pitch) != p.cfg.pitch() {
			C.cudaFree(devPtr)
			return fmt.Errorf("slot %d pitch %d != expected %d", i, int(pitch), p.cfg.pitch())
		}
	}
	return nil
}

// allocResultSlots allocates result buffers in VRAM.
func (p *CUDAGPUPool) allocResultSlots() error {
	p.resultDevPtrs = make([]C.CUdeviceptr, p.cfg.SlotCount)
	for i := 0; i < p.cfg.SlotCount; i++ {
		var devPtr unsafe.Pointer
		rc := C.cudaMalloc(&devPtr, C.size_t(p.cfg.ResultSize))
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaMalloc result slot %d: error %d", i, rc)
		}
		p.resultDevPtrs[i] = C.CUdeviceptr(uintptr(devPtr))
	}
	return nil
}

// exportFrameHandles exports IPC handles for all frame device pointers.
func (p *CUDAGPUPool) exportFrameHandles() error {
	p.frameHandles = make([]CUDAIPCHandle, p.cfg.SlotCount)
	for i := 0; i < p.cfg.SlotCount; i++ {
		var handle C.CUipcMemHandle
		rc := C.cuIpcGetMemHandle(&handle, p.frameDevPtrs[i])
		if rc != C.CUDA_SUCCESS {
			return fmt.Errorf("cuIpcGetMemHandle frame slot %d: error %d", i, rc)
		}
		copy(p.frameHandles[i][:], C.GoBytes(unsafe.Pointer(&handle), 64))
	}
	return nil
}

// exportResultHandles exports IPC handles for all result device pointers.
func (p *CUDAGPUPool) exportResultHandles() error {
	p.resultHandles = make([]CUDAIPCHandle, p.cfg.SlotCount)
	for i := 0; i < p.cfg.SlotCount; i++ {
		var handle C.CUipcMemHandle
		rc := C.cuIpcGetMemHandle(&handle, p.resultDevPtrs[i])
		if rc != C.CUDA_SUCCESS {
			return fmt.Errorf("cuIpcGetMemHandle result slot %d: error %d", i, rc)
		}
		copy(p.resultHandles[i][:], C.GoBytes(unsafe.Pointer(&handle), 64))
	}
	return nil
}

// --- Internal import (worker side) ---

// importFrameHandles imports IPC memory handles from the host.
func (p *CUDAGPUPool) importFrameHandles() error {
	p.frameDevPtrs = make([]C.CUdeviceptr, p.cfg.SlotCount)
	for i := 0; i < p.cfg.SlotCount; i++ {
		var handle C.CUipcMemHandle
		C.memcpy(unsafe.Pointer(&handle), unsafe.Pointer(&p.frameHandles[i][0]), 64)
		var dptr C.CUdeviceptr
		rc := C.cuIpcOpenMemHandle(&dptr, handle, C.CU_IPC_MEM_LAZY_ENABLE_PEER_ACCESS)
		if rc != C.CUDA_SUCCESS {
			return fmt.Errorf("cuIpcOpenMemHandle frame slot %d: error %d", i, rc)
		}
		p.frameDevPtrs[i] = dptr
	}
	return nil
}

// importResultHandles imports IPC memory handles for result slots.
func (p *CUDAGPUPool) importResultHandles() error {
	p.resultDevPtrs = make([]C.CUdeviceptr, p.cfg.SlotCount)
	for i := 0; i < p.cfg.SlotCount; i++ {
		var handle C.CUipcMemHandle
		C.memcpy(unsafe.Pointer(&handle), unsafe.Pointer(&p.resultHandles[i][0]), 64)
		var dptr C.CUdeviceptr
		rc := C.cuIpcOpenMemHandle(&dptr, handle, C.CU_IPC_MEM_LAZY_ENABLE_PEER_ACCESS)
		if rc != C.CUDA_SUCCESS {
			return fmt.Errorf("cuIpcOpenMemHandle result slot %d: error %d", i, rc)
		}
		p.resultDevPtrs[i] = dptr
	}
	return nil
}

// --- IPC events ---

// createEvents creates IPC-capable CUDA events for each slot.
func (p *CUDAGPUPool) createEvents() error {
	n := p.cfg.SlotCount
	p.readyEvents = make([]C.cudaEvent_t, n)
	p.doneEvents = make([]C.cudaEvent_t, n)

	for i := 0; i < n; i++ {
		// cudaEventInterprocess | cudaEventDisableTiming: IPC-capable, no timing overhead.
		flags := C.uint(C.cudaEventInterprocess | C.cudaEventDisableTiming)

		rc := C.cudaEventCreateWithFlags(&p.readyEvents[i], flags)
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaEventCreate ready[%d]: error %d", i, rc)
		}

		rc = C.cudaEventCreateWithFlags(&p.doneEvents[i], flags)
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaEventCreate done[%d]: error %d", i, rc)
		}
	}
	return nil
}

// exportEventHandles exports IPC event handles.
func (p *CUDAGPUPool) exportEventHandles() error {
	n := p.cfg.SlotCount
	p.readyHandles = make([]CUDAIPCEventHandle, n)
	p.doneHandles = make([]CUDAIPCEventHandle, n)

	for i := 0; i < n; i++ {
		var readyH C.cudaIpcEventHandle_t
		rc := C.cudaIpcGetEventHandle(&readyH, p.readyEvents[i])
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaIpcGetEventHandle ready[%d]: error %d", i, rc)
		}
		copy(p.readyHandles[i][:], C.GoBytes(unsafe.Pointer(&readyH), 64))

		var doneH C.cudaIpcEventHandle_t
		rc = C.cudaIpcGetEventHandle(&doneH, p.doneEvents[i])
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaIpcGetEventHandle done[%d]: error %d", i, rc)
		}
		copy(p.doneHandles[i][:], C.GoBytes(unsafe.Pointer(&doneH), 64))
	}
	return nil
}

// openEvents imports IPC events on the worker side.
func (p *CUDAGPUPool) openEvents() error {
	n := p.cfg.SlotCount
	p.readyEvents = make([]C.cudaEvent_t, n)
	p.doneEvents = make([]C.cudaEvent_t, n)

	for i := 0; i < n; i++ {
		var readyH C.cudaIpcEventHandle_t
		C.memcpy(unsafe.Pointer(&readyH), unsafe.Pointer(&p.readyHandles[i][0]), 64)
		rc := C.cudaIpcOpenEventHandle(&p.readyEvents[i], readyH)
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaIpcOpenEventHandle ready[%d]: error %d", i, rc)
		}

		var doneH C.cudaIpcEventHandle_t
		C.memcpy(unsafe.Pointer(&doneH), unsafe.Pointer(&p.doneHandles[i][0]), 64)
		rc = C.cudaIpcOpenEventHandle(&p.doneEvents[i], doneH)
		if rc != C.cudaSuccess {
			return fmt.Errorf("cudaIpcOpenEventHandle done[%d]: error %d", i, rc)
		}
	}
	return nil
}

// --- POSIX shared memory for control region ---

// createControlShm creates the POSIX shared memory segment for the control region
// and initializes the header.
func (p *CUDAGPUPool) createControlShm() error {
	size := GPUPoolControlSize(p.cfg.SlotCount)

	// Clean up any stale segment from a previous crash.
	cname := C.CString(p.ctrlShmName)
	defer C.free(unsafe.Pointer(cname))
	C.shm_unlink(cname)

	fd := C.gpu_shm_create(cname)
	if fd < 0 {
		return fmt.Errorf("shm_open(%s) create failed", p.ctrlShmName)
	}

	if ret := C.ftruncate(fd, C.off_t(size)); ret != 0 {
		C.close(fd)
		C.shm_unlink(cname)
		return fmt.Errorf("ftruncate(%s, %d) failed", p.ctrlShmName, size)
	}

	ptr := C.mmap(nil, C.size_t(size), C.PROT_READ|C.PROT_WRITE, C.MAP_SHARED, fd, 0)
	if ptr == C.MAP_FAILED {
		C.close(fd)
		C.shm_unlink(cname)
		return fmt.Errorf("mmap(%s) failed", p.ctrlShmName)
	}
	C.close(fd)

	// Convert to a Go slice backed by the mmap'd region.
	p.ctrlShm = unsafe.Slice((*byte)(ptr), size)

	ctrl, err := NewGPUPoolControl(p.ctrlShm, p.cfg.SlotCount)
	if err != nil {
		C.munmap(ptr, C.size_t(size))
		C.shm_unlink(cname)
		return err
	}
	ctrl.InitHeader(p.cfg)
	p.ctrl = ctrl

	return nil
}

// openControlShm opens an existing POSIX shared memory control region (worker side).
func (p *CUDAGPUPool) openControlShm() error {
	size := GPUPoolControlSize(p.cfg.SlotCount)

	cname := C.CString(p.ctrlShmName)
	defer C.free(unsafe.Pointer(cname))

	fd := C.gpu_shm_open(cname)
	if fd < 0 {
		return fmt.Errorf("shm_open(%s) open failed", p.ctrlShmName)
	}

	ptr := C.mmap(nil, C.size_t(size), C.PROT_READ|C.PROT_WRITE, C.MAP_SHARED, fd, 0)
	if ptr == C.MAP_FAILED {
		C.close(fd)
		return fmt.Errorf("mmap(%s) failed", p.ctrlShmName)
	}
	C.close(fd)

	p.ctrlShm = unsafe.Slice((*byte)(ptr), size)

	ctrl, err := NewGPUPoolControl(p.ctrlShm, p.cfg.SlotCount)
	if err != nil {
		C.munmap(ptr, C.size_t(size))
		return err
	}
	p.ctrl = ctrl

	return nil
}

// --- Close ---

// Close releases all GPU and shared memory resources.
//
// Host side: frees VRAM allocations, destroys events, unmaps and unlinks shm.
// Worker side: closes IPC handles, unmaps shm (does not unlink — host owns it).
func (p *CUDAGPUPool) Close() error {
	if p == nil {
		return nil
	}

	var errs []error

	if p.isHost {
		// Free frame VRAM only if we allocated it (not zero-copy export).
		if p.ownsFrames {
			for i, dptr := range p.frameDevPtrs {
				if dptr != 0 {
					C.cudaFree(unsafe.Pointer(uintptr(dptr)))
					p.frameDevPtrs[i] = 0
				}
			}
		}
		// Free result VRAM allocations.
		for i, dptr := range p.resultDevPtrs {
			if dptr != 0 {
				C.cudaFree(unsafe.Pointer(uintptr(dptr)))
				p.resultDevPtrs[i] = 0
			}
		}
		// Destroy events (host owns them).
		for i, ev := range p.readyEvents {
			if ev != nil {
				C.cudaEventDestroy(ev)
				p.readyEvents[i] = nil
			}
		}
		for i, ev := range p.doneEvents {
			if ev != nil {
				C.cudaEventDestroy(ev)
				p.doneEvents[i] = nil
			}
		}
	} else {
		// Worker: close imported memory handles.
		for i, dptr := range p.frameDevPtrs {
			if dptr != 0 {
				C.cuIpcCloseMemHandle(dptr)
				p.frameDevPtrs[i] = 0
			}
		}
		for i, dptr := range p.resultDevPtrs {
			if dptr != 0 {
				C.cuIpcCloseMemHandle(dptr)
				p.resultDevPtrs[i] = 0
			}
		}
		// Worker: events opened via IPC are just references, destroy to release.
		for i, ev := range p.readyEvents {
			if ev != nil {
				C.cudaEventDestroy(ev)
				p.readyEvents[i] = nil
			}
		}
		for i, ev := range p.doneEvents {
			if ev != nil {
				C.cudaEventDestroy(ev)
				p.doneEvents[i] = nil
			}
		}
	}

	// Unmap control region shared memory.
	if p.ctrlShm != nil {
		size := C.size_t(len(p.ctrlShm))
		ptr := unsafe.Pointer(&p.ctrlShm[0])
		if ret := C.munmap(ptr, size); ret != 0 {
			errs = append(errs, fmt.Errorf("munmap control shm failed"))
		}
		p.ctrlShm = nil
		p.ctrl = nil
	}

	// Host owns the shm segment — unlink it.
	if p.isHost && p.ctrlShmName != "" {
		cname := C.CString(p.ctrlShmName)
		defer C.free(unsafe.Pointer(cname))
		if ret := C.shm_unlink(cname); ret != 0 {
			errs = append(errs, fmt.Errorf("shm_unlink(%s) failed", p.ctrlShmName))
		}
	}

	return errors.Join(errs...)
}
