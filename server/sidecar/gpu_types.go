package sidecar

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// GPUPoolConfig configures a shared VRAM pool for cross-process GPU frame exchange.
type GPUPoolConfig struct {
	// SlotCount is the number of pre-allocated frame slots in the pool.
	SlotCount int

	// Width is the frame width in pixels.
	Width int

	// Height is the frame height in pixels.
	Height int

	// Pitch is the row pitch in bytes (256-byte aligned for NVENC/Metal).
	// If zero, computed as (Width + 255) &^ 255.
	Pitch int

	// ResultSize is the size in bytes of each result slot. Results are
	// inference outputs (masks, depth maps, etc.) written by the worker
	// and read by the host. If zero, result slots are not allocated.
	ResultSize int

	// Format is the GPU pixel format of pool frames.
	// 0 = NV12 (8-bit 4:2:0), 1 = P210 (10-bit 4:2:2).
	// Uses plain int to avoid importing gpu package from sidecar.
	Format int
}

// NV12FrameSize returns the total size of one NV12 frame: pitch * height * 3/2.
func (c GPUPoolConfig) NV12FrameSize() int {
	p := c.pitch()
	return p * c.Height * 3 / 2
}

// pitch returns the effective pitch, computing it if not set.
func (c GPUPoolConfig) pitch() int {
	if c.Pitch > 0 {
		return c.Pitch
	}
	return (c.Width + 255) &^ 255
}

// Validate checks that the config is well-formed.
func (c GPUPoolConfig) Validate() error {
	if c.SlotCount <= 0 {
		return fmt.Errorf("gpu pool: SlotCount must be > 0")
	}
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("gpu pool: Width and Height must be > 0")
	}
	if c.Pitch > 0 && c.Pitch < c.Width {
		return fmt.Errorf("gpu pool: Pitch (%d) must be >= Width (%d)", c.Pitch, c.Width)
	}
	return nil
}

// GPUPoolSlotState tracks per-slot readiness in shared memory.
// States are written atomically by host and worker to coordinate
// zero-copy frame exchange without locks.
type GPUPoolSlotState uint32

const (
	// SlotIdle means the slot is available for the host to write a new frame.
	SlotIdle GPUPoolSlotState = 0

	// SlotReady means the host has written frame data and the worker can read it.
	// The host must record a GPU event before setting this state.
	SlotReady GPUPoolSlotState = 1

	// SlotProcessing means the worker is running inference on this slot.
	SlotProcessing GPUPoolSlotState = 2

	// SlotDone means the worker has written results and the host can read them.
	// The worker must record a GPU event before setting this state.
	SlotDone GPUPoolSlotState = 3
)

func (s GPUPoolSlotState) String() string {
	switch s {
	case SlotIdle:
		return "idle"
	case SlotReady:
		return "ready"
	case SlotProcessing:
		return "processing"
	case SlotDone:
		return "done"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// GPUPoolControlHeader is the shared memory layout for the pool control
// channel. Both host and worker mmap this region and use atomic operations
// to coordinate slot states. This avoids needing the Unix socket for
// per-frame signaling (the socket is only for control messages).
//
// Memory layout (64 bytes per slot, cache-line aligned):
//
//	offset 0:    magic     uint32   (0x47505530 = "GPU0")
//	offset 4:    version   uint32   (1)
//	offset 8:    slotCount uint32
//	offset 12:   width     uint32
//	offset 16:   height    uint32
//	offset 20:   pitch     uint32
//	offset 24:   reserved  [40]byte
//	offset 64:   slot[0].state      uint32 (atomic)
//	offset 68:   slot[0].pad       uint32 (alignment padding)
//	offset 72:   slot[0].pts       int64  (atomic, 8-byte aligned)
//	offset 80:   slot[0].sourceKey [32]byte (null-terminated UTF-8)
//	offset 112:  slot[0].pad2      [16]byte
//	offset 128:  slot[1].state     uint32
//	...
const (
	gpuPoolMagic   = 0x47505530 // "GPU0"
	gpuPoolVersion = 1

	gpuPoolHeaderSize = 64 // bytes before slot array
	gpuPoolSlotStride = 64 // bytes per slot (cache-line aligned)
)

// GPUPoolControl provides atomic access to the shared control region.
type GPUPoolControl struct {
	data      []byte
	slotCount int
}

// NewGPUPoolControl wraps an existing mmap'd region as a pool control.
// The region must be at least gpuPoolHeaderSize + slotCount*gpuPoolSlotStride bytes.
func NewGPUPoolControl(data []byte, slotCount int) (*GPUPoolControl, error) {
	needed := gpuPoolHeaderSize + slotCount*gpuPoolSlotStride
	if len(data) < needed {
		return nil, fmt.Errorf("gpu pool control: region too small: %d < %d", len(data), needed)
	}
	return &GPUPoolControl{data: data, slotCount: slotCount}, nil
}

// GPUPoolControlSize returns the total size needed for the control region.
func GPUPoolControlSize(slotCount int) int {
	return gpuPoolHeaderSize + slotCount*gpuPoolSlotStride
}

// InitHeader writes the magic, version, and config fields. Called once by the host.
func (c *GPUPoolControl) InitHeader(cfg GPUPoolConfig) {
	*(*uint32)(unsafe.Pointer(&c.data[0])) = gpuPoolMagic
	*(*uint32)(unsafe.Pointer(&c.data[4])) = gpuPoolVersion
	*(*uint32)(unsafe.Pointer(&c.data[8])) = uint32(cfg.SlotCount)
	*(*uint32)(unsafe.Pointer(&c.data[12])) = uint32(cfg.Width)
	*(*uint32)(unsafe.Pointer(&c.data[16])) = uint32(cfg.Height)
	*(*uint32)(unsafe.Pointer(&c.data[20])) = uint32(cfg.pitch())
}

// ValidateHeader checks magic and version. Called by the worker after mapping.
func (c *GPUPoolControl) ValidateHeader() error {
	magic := *(*uint32)(unsafe.Pointer(&c.data[0]))
	if magic != gpuPoolMagic {
		return fmt.Errorf("gpu pool control: bad magic: 0x%08x (want 0x%08x)", magic, gpuPoolMagic)
	}
	version := *(*uint32)(unsafe.Pointer(&c.data[4]))
	if version != gpuPoolVersion {
		return fmt.Errorf("gpu pool control: version %d (want %d)", version, gpuPoolVersion)
	}
	return nil
}

// slotStatePtr returns a pointer to the state field of slot i.
// Panics if i is out of range to prevent silent memory corruption.
func (c *GPUPoolControl) slotStatePtr(i int) *uint32 {
	if i < 0 || i >= c.slotCount {
		panic(fmt.Sprintf("gpu pool: slot index %d out of range [0, %d)", i, c.slotCount))
	}
	offset := gpuPoolHeaderSize + i*gpuPoolSlotStride
	return (*uint32)(unsafe.Pointer(&c.data[offset]))
}

// slotPTSPtr returns a pointer to the PTS field of slot i.
// PTS is at offset +8 within the slot (after state uint32 + 4 bytes padding)
// to ensure 8-byte alignment for atomic int64 operations on ARM64.
// Panics if i is out of range.
func (c *GPUPoolControl) slotPTSPtr(i int) *int64 {
	if i < 0 || i >= c.slotCount {
		panic(fmt.Sprintf("gpu pool: slot index %d out of range [0, %d)", i, c.slotCount))
	}
	offset := gpuPoolHeaderSize + i*gpuPoolSlotStride + 8
	return (*int64)(unsafe.Pointer(&c.data[offset]))
}

// LoadState atomically loads the state of slot i.
func (c *GPUPoolControl) LoadState(i int) GPUPoolSlotState {
	return GPUPoolSlotState(atomic.LoadUint32(c.slotStatePtr(i)))
}

// CompareAndSwapState atomically changes slot i from old to new state.
// Returns true if the swap succeeded.
func (c *GPUPoolControl) CompareAndSwapState(i int, old, new GPUPoolSlotState) bool {
	return atomic.CompareAndSwapUint32(c.slotStatePtr(i), uint32(old), uint32(new))
}

// SetPTS atomically sets the PTS for slot i.
func (c *GPUPoolControl) SetPTS(i int, pts int64) {
	atomic.StoreInt64(c.slotPTSPtr(i), pts)
}

// LoadPTS atomically loads the PTS for slot i.
func (c *GPUPoolControl) LoadPTS(i int) int64 {
	return atomic.LoadInt64(c.slotPTSPtr(i))
}

// MarkReady transitions slot i from Idle to Ready (host → worker).
// Returns false if the slot was not in Idle state.
func (c *GPUPoolControl) MarkReady(i int, pts int64) bool {
	c.SetPTS(i, pts)
	return c.CompareAndSwapState(i, SlotIdle, SlotReady)
}

// MarkProcessing transitions slot i from Ready to Processing (worker).
// Returns false if the slot was not in Ready state.
func (c *GPUPoolControl) MarkProcessing(i int) bool {
	return c.CompareAndSwapState(i, SlotReady, SlotProcessing)
}

// MarkDone transitions slot i from Processing to Done (worker → host).
// Returns false if the slot was not in Processing state.
func (c *GPUPoolControl) MarkDone(i int) bool {
	return c.CompareAndSwapState(i, SlotProcessing, SlotDone)
}

// MarkIdle transitions slot i from Done to Idle (host reclaims).
// Returns false if the slot was not in Done state.
func (c *GPUPoolControl) MarkIdle(i int) bool {
	return c.CompareAndSwapState(i, SlotDone, SlotIdle)
}

// sourceKeyOffset is the byte offset of the source key within a slot.
const sourceKeyOffset = 16 // after state(4) + pad(4) + pts(8)
const sourceKeyMaxLen = 32

// SetSourceKey writes a null-terminated source key into the slot's shared memory.
// Must be called BEFORE MarkReady (the sidecar reads it after seeing SlotReady).
// Keys longer than 31 bytes are truncated.
func (c *GPUPoolControl) SetSourceKey(i int, key string) {
	if i < 0 || i >= c.slotCount {
		return
	}
	offset := gpuPoolHeaderSize + i*gpuPoolSlotStride + sourceKeyOffset
	// Zero the field first.
	for j := 0; j < sourceKeyMaxLen; j++ {
		c.data[offset+j] = 0
	}
	// Copy key bytes (truncate at 31 to leave room for null terminator).
	n := len(key)
	if n > sourceKeyMaxLen-1 {
		n = sourceKeyMaxLen - 1
	}
	copy(c.data[offset:offset+n], key)
}

// LoadSourceKey reads the null-terminated source key from a slot's shared memory.
func (c *GPUPoolControl) LoadSourceKey(i int) string {
	if i < 0 || i >= c.slotCount {
		return ""
	}
	offset := gpuPoolHeaderSize + i*gpuPoolSlotStride + sourceKeyOffset
	// Find null terminator.
	for j := 0; j < sourceKeyMaxLen; j++ {
		if c.data[offset+j] == 0 {
			return string(c.data[offset : offset+j])
		}
	}
	return string(c.data[offset : offset+sourceKeyMaxLen])
}

// FindSlot scans for a slot in the given state. Returns -1 if none found.
// Not atomic across slots — caller should retry or use in a poll loop.
func (c *GPUPoolControl) FindSlot(state GPUPoolSlotState) int {
	for i := 0; i < c.slotCount; i++ {
		if c.LoadState(i) == state {
			return i
		}
	}
	return -1
}
