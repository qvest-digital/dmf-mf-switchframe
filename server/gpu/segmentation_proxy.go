//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/sidecar"
)

// maxInFlight limits how many GPU pool frames can be Ref'd for sidecar
// inference at once. With a 32-slot FramePool, this preserves 8 frames of
// pipeline headroom to avoid cudaMallocPitch on the hot path.
const maxInFlight = 24

// SegmentationProxy implements Segmenter by delegating inference to the AI
// sidecar process. It uses the sidecar SDK's Host for control messages and
// CUDAGPUPool for zero-copy GPU frame sharing via CUDA IPC.
//
// Data flow:
//  1. Pipeline writes NV12 frame to FramePool slot (existing path)
//  2. Segment() finds the pool slot for the frame, marks it Ready
//  3. Sidecar polls for Ready slots, runs inference on the shared VRAM
//  4. Sidecar writes mask, exports IPC handle via mask_ready message
//  5. Proxy imports handle once, wraps in GPUFrame for lock-free pipeline reads
type SegmentationProxy struct {
	host *sidecar.Host
	pool *sidecar.CUDAGPUPool

	// FramePool DevPtr → GPU pool slot index. Built once at construction.
	devPtrToSlot map[uintptr]int

	mu             sync.RWMutex
	sources        map[string]*proxySource
	pendingConfigs map[string]float32

	// In-flight frames: slot → Ref'd frame (prevents FramePool from
	// recycling memory while the sidecar reads it).
	inFlightMu sync.Mutex
	inFlight   map[int]*GPUFrame

	doneCh chan struct{}
	closed atomic.Bool
}

// proxySource holds per-source state for the proxy.
type proxySource struct {
	width, height int
	smoothing     float32

	// Imported mask device pointer from sidecar's mask_ready message.
	// Zero until the first mask is received. Closed on DisableSource.
	maskDevPtr uintptr

	// GPUFrame wrapping maskDevPtr — manually managed (not from pool).
	maskFrame *GPUFrame

	// Latest mask for lock-free pipeline reads.
	mask atomic.Pointer[GPUFrame]
}

// NewSegmentationProxy creates a proxy that delegates segmentation to the AI sidecar.
//
// The pool must be created with NewCUDAGPUPoolHostFromPtrs, exporting the
// pipeline's FramePool device pointers. This enables zero-copy: the sidecar
// reads frames directly from the same physical VRAM.
//
// The caller must wire host.OnMessage to call proxy.HandleMessage for
// mask_ready messages.
func NewSegmentationProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool) *SegmentationProxy {
	p := &SegmentationProxy{
		host:           host,
		pool:           pool,
		devPtrToSlot:   sidecar.BuildDevPtrToSlotMap(pool),
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
		doneCh:         make(chan struct{}),
	}
	p.startWatchdog()
	return p
}

// startWatchdog runs a background goroutine that periodically recovers
// stale GPU pool slots. This handles sidecar hangs (not just crashes).
func (p *SegmentationProxy) startWatchdog() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.doneCh:
				return
			case <-ticker.C:
				if p.closed.Load() {
					return
				}
				p.RecoverStaleSlots()
			}
		}
	}()
}

// EnableSource sends enable_segmentation to the sidecar and creates per-source state.
func (p *SegmentationProxy) EnableSource(key string, w, h int, smoothing float32) error {
	if p == nil || p.closed.Load() {
		return fmt.Errorf("gpu: segmentation proxy: not available")
	}

	// Send command to sidecar.
	err := p.host.Send(&sidecar.Message{
		Cmd: sidecar.CmdEnableSegmentation,
		Config: map[string]any{
			"source_id": key,
			"width":     w,
			"height":    h,
			"smoothing": smoothing,
		},
	})
	if err != nil {
		return fmt.Errorf("gpu: segmentation proxy: enable %s: %w", key, err)
	}

	p.mu.Lock()
	// Clean up any existing source state.
	if old, exists := p.sources[key]; exists {
		p.cleanupSource(old)
	}
	p.sources[key] = &proxySource{width: w, height: h, smoothing: smoothing}
	p.mu.Unlock()

	slog.Info("gpu: segmentation proxy: enabled source", "source", key, "size", [2]int{w, h})
	return nil
}

// DisableSource sends disable_segmentation and cleans up per-source state.
func (p *SegmentationProxy) DisableSource(key string) {
	if p == nil {
		return
	}

	_ = p.host.Send(&sidecar.Message{
		Cmd:    sidecar.CmdDisableSegmentation,
		Config: map[string]any{"source_id": key},
	})

	p.mu.Lock()
	src, exists := p.sources[key]
	if exists {
		delete(p.sources, key)
		p.cleanupSource(src)
	}
	delete(p.pendingConfigs, key)
	p.mu.Unlock()

	if exists {
		slog.Info("gpu: segmentation proxy: disabled source", "source", key)
	}
}

// SetPendingConfig stores a deferred segmentation config for a source.
func (p *SegmentationProxy) SetPendingConfig(key string, smoothing float32) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.pendingConfigs[key] = smoothing
	p.mu.Unlock()
}

// HasPendingConfig returns true if a deferred config exists but no active session.
func (p *SegmentationProxy) HasPendingConfig(key string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	_, hasPending := p.pendingConfigs[key]
	_, hasSource := p.sources[key]
	p.mu.RUnlock()
	return hasPending && !hasSource
}

// PendingSmoothing returns the smoothing value for a pending config.
func (p *SegmentationProxy) PendingSmoothing(key string) float32 {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	s := p.pendingConfigs[key]
	p.mu.RUnlock()
	return s
}

// ClearPendingConfig removes the deferred config for a source.
func (p *SegmentationProxy) ClearPendingConfig(key string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.pendingConfigs, key)
	p.mu.Unlock()
}

// IsEnabled returns true if segmentation is active for the source.
func (p *SegmentationProxy) IsEnabled(key string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	_, exists := p.sources[key]
	p.mu.RUnlock()
	return exists
}

// Segment submits a frame for async segmentation in the sidecar.
//
// It finds the GPU pool slot for the frame's DevPtr, marks it Ready,
// and sends an infer command over the control socket so the sidecar
// knows which source the frame belongs to.
//
// The frame is Ref'd to prevent the FramePool from recycling the VRAM
// while the sidecar reads it. It's Released when the slot becomes Done.
func (p *SegmentationProxy) Segment(key string, frame *GPUFrame) {
	if p == nil || frame == nil || p.closed.Load() {
		return
	}

	p.mu.RLock()
	_, enabled := p.sources[key]
	p.mu.RUnlock()
	if !enabled {
		return
	}

	// Find pool slot for this frame.
	slot, ok := p.devPtrToSlot[uintptr(frame.DevPtr)]
	if !ok {
		return
	}

	ctrl := p.pool.Control()

	// NOTE: We intentionally do NOT record the host-side ready event here.
	// Segment() is called from the pipeline AFTER the frame has been fully
	// processed (post-ingest): the GPU upload + pipeline node processing are
	// already complete, and the frame data in VRAM is stable. The ready event
	// is recorded by the pipeline's GPU upload path (before the frame reaches
	// the source manager's cache), so by the time Segment() runs, the event
	// has already been recorded and the sidecar can safely WaitReadyEvent().
	//
	// If a future code path calls Segment() while GPU writes are still in
	// flight, the ready event MUST be recorded on the host's CUDA stream
	// before this point.

	// Write source key to shared memory BEFORE marking Ready.
	// The sidecar reads this after seeing SlotReady to dispatch to the
	// correct per-source session. This avoids a per-frame Host.Send call
	// (JSON marshal + mutex + socket write) on the hot path.
	ctrl.SetSourceKey(slot, key)

	// MarkReady sets PTS BEFORE the state transition (atomic ordering matters —
	// the sidecar reads PTS immediately after seeing SlotReady).
	if !ctrl.MarkReady(slot, frame.PTS) {
		return // slot busy, skip this frame
	}

	// Check pool exhaustion before Ref'ing the frame.
	p.inFlightMu.Lock()
	inFlightCount := len(p.inFlight)
	p.inFlightMu.Unlock()

	if inFlightCount >= maxInFlight {
		// Revert: mark slot back to Idle since we can't hold more frames.
		ctrl.CompareAndSwapState(slot, sidecar.SlotReady, sidecar.SlotIdle)
		slog.Warn("gpu: segmentation proxy: pool exhaustion, skipping frame",
			"in_flight", inFlightCount, "max", maxInFlight)
		return
	}

	// Ref frame to prevent FramePool from recycling.
	frame.Ref()
	p.inFlightMu.Lock()
	if old := p.inFlight[slot]; old != nil {
		old.Release()
	}
	p.inFlight[slot] = frame
	p.inFlightMu.Unlock()
}

// MaskForSource returns the latest segmentation mask for a source with a
// reference taken; the caller MUST Release() the returned frame when done
// (matching the SegmentationEngine contract). Proxy mask frames wrap
// IPC-imported pointers and are created with one owning reference, so a
// balanced Ref/Release by callers never frees the imported memory —
// teardown closes the IPC handle explicitly.
func (p *SegmentationProxy) MaskForSource(key string) *GPUFrame {
	if p == nil {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	src, exists := p.sources[key]
	if !exists {
		return nil
	}

	mask := src.mask.Load()
	if mask != nil {
		mask.Ref()
	}
	return mask
}

// MaskDevPtrForSource returns the imported mask device pointer for a source.
// Returns 0 if no mask has been imported yet.
func (p *SegmentationProxy) MaskDevPtrForSource(key string) uintptr {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	src, exists := p.sources[key]
	p.mu.RUnlock()
	if !exists {
		return 0
	}
	return src.maskDevPtr
}

// HandleMessage processes a message from the AI sidecar.
// Must be called from the host's OnMessage callback.
func (p *SegmentationProxy) HandleMessage(msg *sidecar.Message) {
	if p == nil || msg == nil {
		return
	}

	switch msg.Type {
	case sidecar.MsgMaskReady:
		p.handleMaskReady(msg)
	case sidecar.MsgSlotDone:
		p.handleSlotDone(msg)
	}
}

// handleMaskReady imports a per-source mask IPC handle from the sidecar.
// Called once per source when the sidecar first allocates the mask buffer.
func (p *SegmentationProxy) handleMaskReady(msg *sidecar.Message) {
	mh, err := sidecar.ParseMaskHandleMessage(msg)
	if err != nil {
		slog.Warn("gpu: segmentation proxy: parse mask_ready", "error", err)
		return
	}
	if mh == nil {
		return
	}

	// Check source exists before the (potentially slow) IPC import.
	p.mu.RLock()
	_, exists := p.sources[mh.SourceID]
	p.mu.RUnlock()
	if !exists {
		slog.Warn("gpu: segmentation proxy: mask_ready for unknown source", "source", mh.SourceID)
		return
	}

	// Import the IPC handle to get a local device pointer.
	devPtr, err := sidecar.ImportIPCHandle(mh.Handle)
	if err != nil {
		slog.Error("gpu: segmentation proxy: import mask handle", "source", mh.SourceID, "error", err)
		return
	}

	// Re-check under write lock — source may have been disabled concurrently.
	p.mu.Lock()
	src, stillExists := p.sources[mh.SourceID]
	if !stillExists {
		p.mu.Unlock()
		sidecar.CloseIPCHandle(devPtr)
		slog.Warn("gpu: segmentation proxy: source removed during mask import", "source", mh.SourceID)
		return
	}

	// Close any previously imported handle for this source.
	if src.maskDevPtr != 0 {
		sidecar.CloseIPCHandle(src.maskDevPtr)
	}
	src.maskDevPtr = devPtr
	src.maskFrame = &GPUFrame{
		DevPtr: devPtrFromUintptr(devPtr),
		Width:  mh.Width,
		Height: mh.Height,
		Pitch:  mh.Width, // uint8 mask, stride = width
	}
	// One owning reference: MaskForSource callers Ref/Release around it, so
	// the count never reaches zero (Release at zero would cudaFree, which is
	// wrong for IPC-imported memory — teardown uses CloseIPCHandle instead).
	src.maskFrame.refs.Store(1)
	src.mask.Store(src.maskFrame)
	p.mu.Unlock()

	slog.Info("gpu: segmentation proxy: mask imported",
		"source", mh.SourceID, "size", [2]int{mh.Width, mh.Height})
}

// handleSlotDone releases an in-flight frame when the sidecar finishes
// processing a slot. The sidecar sends this after marking the slot Done
// in shared memory.
func (p *SegmentationProxy) handleSlotDone(msg *sidecar.Message) {
	// Extract slot index from message data.
	data, ok := msg.Data.(map[string]any)
	if !ok {
		return
	}
	slotF, ok := data["slot"].(float64)
	if !ok {
		return
	}
	slot := int(slotF)
	if slot < 0 || (p.pool != nil && slot >= p.pool.SlotCount()) {
		return
	}

	// Release the in-flight frame.
	p.inFlightMu.Lock()
	frame := p.inFlight[slot]
	delete(p.inFlight, slot)
	p.inFlightMu.Unlock()

	if frame != nil {
		frame.Release()
	}

	// Mark slot Idle so it can be reused.
	if p.pool != nil {
		if ctrl := p.pool.Control(); ctrl != nil {
			ctrl.CompareAndSwapState(slot, sidecar.SlotDone, sidecar.SlotIdle)
		}
	}
}

// RecoverStaleSlots scans for slots stuck in Done or Processing state and
// resets them to Idle, releasing any in-flight frames. Call this from the
// Host's OnRestart callback after a sidecar crash to prevent slot leaks.
func (p *SegmentationProxy) RecoverStaleSlots() {
	if p == nil || p.pool == nil {
		return
	}
	ctrl := p.pool.Control()
	slotCount := p.pool.SlotCount()

	for i := 0; i < slotCount; i++ {
		state := ctrl.LoadState(i)
		if state == sidecar.SlotDone || state == sidecar.SlotProcessing || state == sidecar.SlotReady {
			// Force back to Idle.
			ctrl.CompareAndSwapState(i, state, sidecar.SlotIdle)

			// Release any in-flight frame.
			p.inFlightMu.Lock()
			frame := p.inFlight[i]
			delete(p.inFlight, i)
			p.inFlightMu.Unlock()
			if frame != nil {
				frame.Release()
			}
		}
	}
	slog.Info("gpu: segmentation proxy: recovered stale slots after sidecar restart")
}

// ReenableSources re-sends enable_segmentation for all active sources to the
// sidecar. Call this from OnRestart after RecoverStaleSlots to restore state
// that was lost when the sidecar process restarted.
func (p *SegmentationProxy) ReenableSources() {
	if p == nil || p.host == nil {
		return
	}

	p.mu.RLock()
	sources := make(map[string]*proxySource, len(p.sources))
	for k, v := range p.sources {
		sources[k] = v
	}
	p.mu.RUnlock()

	for key, src := range sources {
		// Clear the old imported mask (sidecar's mask buffer is gone).
		p.mu.Lock()
		if src.maskDevPtr != 0 {
			sidecar.CloseIPCHandle(src.maskDevPtr)
			src.maskDevPtr = 0
		}
		src.maskFrame = nil
		src.mask.Store(nil)
		p.mu.Unlock()

		// Re-send enable command — sidecar will allocate new mask buffer
		// and send a fresh mask_ready.
		_ = p.host.Send(&sidecar.Message{
			Cmd: sidecar.CmdEnableSegmentation,
			Config: map[string]any{
				"source_id": key,
				"width":     src.width,
				"height":    src.height,
				"smoothing": src.smoothing,
			},
		})
		slog.Info("gpu: segmentation proxy: re-enabled source after restart", "source", key)
	}
}

// Close releases all proxy resources.
func (p *SegmentationProxy) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	if p.doneCh != nil {
		close(p.doneCh)
	}

	p.mu.Lock()
	sources := p.sources
	p.sources = make(map[string]*proxySource)
	p.pendingConfigs = make(map[string]float32)
	p.mu.Unlock()

	for _, src := range sources {
		p.cleanupSource(src)
	}

	// Release all in-flight frames.
	p.inFlightMu.Lock()
	for slot, frame := range p.inFlight {
		if frame != nil {
			frame.Release()
		}
		delete(p.inFlight, slot)
	}
	p.inFlightMu.Unlock()

	slog.Info("gpu: segmentation proxy: closed")
}

// cleanupSource releases GPU resources for a source.
// The caller must ensure exclusive access to src (either by holding p.mu
// or by having removed src from p.sources so no concurrent access is possible).
func (p *SegmentationProxy) cleanupSource(src *proxySource) {
	src.mask.Store(nil)
	if src.maskDevPtr != 0 {
		sidecar.CloseIPCHandle(src.maskDevPtr)
		src.maskDevPtr = 0
	}
	src.maskFrame = nil
}

// devPtrFromUintptr converts a uintptr to C.CUdeviceptr.
func devPtrFromUintptr(ptr uintptr) C.CUdeviceptr {
	return C.CUdeviceptr(ptr)
}

// Compile-time check that SegmentationProxy implements Segmenter.
var _ Segmenter = (*SegmentationProxy)(nil)
