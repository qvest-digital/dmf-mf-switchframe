//go:build cgo && cuda

package arinfer

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

// maxInFlight limits how many GPU pool frames can be Ref'd for sidecar
// inference at once. With a 32-slot FramePool, this preserves 8 frames of
// pipeline headroom to avoid cudaMallocPitch on the hot path.
const maxInFlight = 24

// InferenceProxy implements Inferrer by delegating depth estimation and SAM
// segmentation to the AI sidecar process. Uses CUDA IPC for zero-copy
// GPU memory sharing of occlusion map results (mask + depth pointers).
//
// The proxy sends commands (enable/disable depth/SAM, prompts) over the
// control socket and receives occlusion_ready messages with IPC handles
// for the sidecar's mask and depth buffers. ProcessFrame submits the
// program frame via the shared GPU pool.
type InferenceProxy struct {
	host *sidecar.Host
	pool *sidecar.CUDAGPUPool

	// FramePool DevPtr → GPU pool slot index. Built once at construction.
	devPtrToSlot map[uintptr]int

	// FramePool DevPtr → GPUFrame. Built once at construction from the
	// FramePool's pre-allocated frames. Used to Ref/Release frames while
	// the sidecar reads them asynchronously, preventing the FramePool from
	// recycling VRAM out from under the sidecar.
	devPtrToFrame map[uintptr]*gpu.GPUFrame

	mu           sync.RWMutex
	depthEnabled bool
	samEnabled   bool
	prompts      []SAMPrompt

	// IPC-imported device pointers for occlusion results.
	maskDevPtr  uintptr // uint8 per-pixel confidence mask
	depthDevPtr uintptr // float16 depth map
	maskW, maskH   int
	depthW, depthH int

	lastOcclusion atomic.Pointer[OcclusionMap]

	// In-flight frame tracking: slot → Ref'd GPUFrame. Prevents the
	// FramePool from recycling memory while the sidecar reads it.
	inFlightMu sync.Mutex
	inFlight   map[int]*gpu.GPUFrame

	// Stats.
	framesProcessed atomic.Int64
	framesSkipped   atomic.Int64

	doneCh chan struct{}
	closed atomic.Bool
}

// NewInferenceProxy creates a proxy that delegates AR inference to the AI sidecar.
// The caller must wire host.OnMessage to call proxy.HandleMessage.
//
// The framePool parameter provides access to the pre-allocated GPUFrame objects.
// The proxy builds a DevPtr→GPUFrame map so it can Ref frames before handing
// them to the sidecar and Release them when processing completes, preventing
// the FramePool from recycling VRAM while the sidecar reads it.
func NewInferenceProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool, framePool *gpu.FramePool) *InferenceProxy {
	// Build DevPtr → GPUFrame map from the FramePool's pre-allocated frames.
	// AllDevPtrs and AllFrames return elements in the same order (both iterate p.all).
	devPtrToFrame := make(map[uintptr]*gpu.GPUFrame)
	if framePool != nil {
		ptrs := framePool.AllDevPtrs()
		frames := framePool.AllFrames()
		for i := 0; i < len(ptrs) && i < len(frames); i++ {
			if ptrs[i] != 0 {
				devPtrToFrame[ptrs[i]] = frames[i]
			}
		}
	}

	p := &InferenceProxy{
		host:          host,
		pool:          pool,
		devPtrToSlot:  sidecar.BuildDevPtrToSlotMap(pool),
		devPtrToFrame: devPtrToFrame,
		inFlight:      make(map[int]*gpu.GPUFrame),
		doneCh:        make(chan struct{}),
	}
	p.startWatchdog()
	return p
}

// startWatchdog runs a background goroutine that periodically recovers
// stale GPU pool slots. This handles sidecar hangs (not just crashes).
func (p *InferenceProxy) startWatchdog() {
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

// ProcessFrame submits the program frame for async depth+SAM inference.
// The frame's underlying GPU memory is Ref'd to prevent the FramePool from
// recycling it while the sidecar reads it. Released on ar_slot_done.
func (p *InferenceProxy) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if p == nil || p.closed.Load() {
		return
	}

	p.mu.RLock()
	active := p.depthEnabled || p.samEnabled
	p.mu.RUnlock()
	if !active {
		return
	}

	// Find pool slot for this frame.
	slot, ok := p.devPtrToSlot[framePtr]
	if !ok {
		p.framesSkipped.Add(1)
		return
	}

	if p.pool == nil {
		return
	}
	ctrl := p.pool.Control()
	if ctrl == nil {
		return
	}

	// Write source key to shared memory BEFORE CAS. The sidecar reads it
	// after seeing SlotReady. sidecar.SourceKeyAR indicates AR inference (depth+SAM).
	//
	// Ready event: NOT recorded here. ProcessFrame is called from the
	// switcher's program frame callback AFTER the GPU pipeline has fully
	// processed the frame (encode node complete). The pipeline's GPU upload
	// path records the ready event during frame acquisition, and by this
	// point all GPU writes are long complete. The sidecar's WaitReadyEvent
	// will either see the previously recorded event or no-op (safe either way
	// since the frame data is stable in VRAM).
	ctrl.SetSourceKey(slot, sidecar.SourceKeyAR)

	if !ctrl.CompareAndSwapState(slot, sidecar.SlotIdle, sidecar.SlotReady) {
		p.framesSkipped.Add(1)
		return // slot busy
	}

	// Check pool exhaustion before Ref'ing the frame.
	p.inFlightMu.Lock()
	inFlightCount := len(p.inFlight)
	p.inFlightMu.Unlock()

	if inFlightCount >= maxInFlight {
		// Revert: mark slot back to Idle since we can't hold more frames.
		ctrl.CompareAndSwapState(slot, sidecar.SlotReady, sidecar.SlotIdle)
		p.framesSkipped.Add(1)
		slog.Warn("arinfer proxy: pool exhaustion, skipping frame",
			"in_flight", inFlightCount, "max", maxInFlight)
		return
	}

	// Ref the GPUFrame to prevent the FramePool from recycling this VRAM
	// while the sidecar reads it asynchronously.
	frame := p.devPtrToFrame[framePtr]
	if frame != nil {
		frame.Ref()
	}

	p.inFlightMu.Lock()
	if old := p.inFlight[slot]; old != nil {
		old.Release()
	}
	p.inFlight[slot] = frame
	p.inFlightMu.Unlock()

	ctrl.SetPTS(slot, 0) // program frame PTS not needed for AR
}

// LastOcclusion returns the most recently computed occlusion map.
func (p *InferenceProxy) LastOcclusion() *OcclusionMap {
	if p == nil {
		return nil
	}
	return p.lastOcclusion.Load()
}

// IsActive returns true if depth or SAM is enabled.
func (p *InferenceProxy) IsActive() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	active := p.depthEnabled || p.samEnabled
	p.mu.RUnlock()
	return active
}

// Hook returns a ProcessFrameHook for wiring into the switcher.
func (p *InferenceProxy) Hook() ProcessFrameHook {
	return p.ProcessFrame
}

// EnableDepth sends enable_depth to the sidecar.
func (p *InferenceProxy) EnableDepth(planPath string) error {
	if p == nil || p.closed.Load() {
		return ErrDisabled
	}

	err := p.host.Send(&sidecar.Message{
		Cmd:    sidecar.CmdEnableDepth,
		Config: map[string]any{"plan_path": planPath},
	})
	if err != nil {
		return fmt.Errorf("arinfer proxy: enable depth: %w", err)
	}

	p.mu.Lock()
	p.depthEnabled = true
	p.mu.Unlock()

	slog.Info("arinfer proxy: depth enabled")
	return nil
}

// DisableDepth sends disable_depth to the sidecar.
func (p *InferenceProxy) DisableDepth() {
	if p == nil {
		return
	}
	_ = p.host.Send(&sidecar.Message{Cmd: sidecar.CmdDisableDepth})

	p.mu.Lock()
	p.depthEnabled = false
	p.mu.Unlock()

	// Clear stale occlusion data.
	p.lastOcclusion.Store(nil)
	slog.Info("arinfer proxy: depth disabled")
}

// EnableSAM sends enable_sam to the sidecar.
func (p *InferenceProxy) EnableSAM(encoderPlanPath, decoderPlanPath string) error {
	if p == nil || p.closed.Load() {
		return ErrDisabled
	}

	err := p.host.Send(&sidecar.Message{
		Cmd: sidecar.CmdEnableSAM,
		Config: map[string]any{
			"encoder_plan_path": encoderPlanPath,
			"decoder_plan_path": decoderPlanPath,
		},
	})
	if err != nil {
		return fmt.Errorf("arinfer proxy: enable SAM: %w", err)
	}

	p.mu.Lock()
	p.samEnabled = true
	p.mu.Unlock()

	slog.Info("arinfer proxy: SAM enabled")
	return nil
}

// DisableSAM sends disable_sam to the sidecar.
func (p *InferenceProxy) DisableSAM() {
	if p == nil {
		return
	}
	_ = p.host.Send(&sidecar.Message{Cmd: sidecar.CmdDisableSAM})

	p.mu.Lock()
	p.samEnabled = false
	p.prompts = nil
	p.mu.Unlock()

	slog.Info("arinfer proxy: SAM disabled")
}

// AddPrompt sends add_prompt to the sidecar.
func (p *InferenceProxy) AddPrompt(prompt SAMPrompt) error {
	if p == nil || p.closed.Load() {
		return ErrDisabled
	}

	p.mu.RLock()
	samEnabled := p.samEnabled
	p.mu.RUnlock()
	if !samEnabled {
		return ErrNotReady
	}

	err := p.host.Send(&sidecar.Message{
		Cmd:    sidecar.CmdAddPrompt,
		Config: prompt,
	})
	if err != nil {
		return fmt.Errorf("arinfer proxy: add prompt: %w", err)
	}

	p.mu.Lock()
	p.prompts = append(p.prompts, prompt)
	p.mu.Unlock()
	return nil
}

// RemovePrompt sends remove_prompt to the sidecar.
func (p *InferenceProxy) RemovePrompt(id string) error {
	if p == nil || p.closed.Load() {
		return ErrDisabled
	}

	p.mu.RLock()
	samEnabled := p.samEnabled
	p.mu.RUnlock()
	if !samEnabled {
		return ErrNotReady
	}

	err := p.host.Send(&sidecar.Message{
		Cmd:    sidecar.CmdRemovePrompt,
		Config: map[string]any{"id": id},
	})
	if err != nil {
		return fmt.Errorf("arinfer proxy: remove prompt: %w", err)
	}

	p.mu.Lock()
	for i, pr := range p.prompts {
		if pr.ID == id {
			p.prompts = append(p.prompts[:i], p.prompts[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	return nil
}

// Prompts returns a copy of the active SAM prompts.
func (p *InferenceProxy) Prompts() []SAMPrompt {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.prompts) == 0 {
		return nil
	}
	out := make([]SAMPrompt, len(p.prompts))
	copy(out, p.prompts)
	return out
}

// Status returns a snapshot of the proxy's state.
func (p *InferenceProxy) Status() Status {
	if p == nil {
		return Status{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Status{
		DepthReady:      p.depthEnabled,
		SAMReady:        p.samEnabled,
		FramesProcessed: p.framesProcessed.Load(),
		FramesSkipped:   p.framesSkipped.Load(),
	}
}

// DepthAtPixel returns a default depth — real sampling requires GPU access
// which is in the sidecar process.
func (p *InferenceProxy) DepthAtPixel(x, y, frameW, frameH int) (float64, error) {
	if p == nil {
		return 2.0, nil
	}
	occ := p.lastOcclusion.Load()
	if occ == nil || occ.DepthPtr == 0 {
		return 2.0, nil
	}
	// Real depth sampling would require either:
	// 1. Sending an RPC to the sidecar to sample the depth buffer
	// 2. Downloading the depth map to host memory
	// For now, return default. This only affects AR object placement.
	return 2.0, nil
}

// HandleMessage processes messages from the AI sidecar.
// Must be called from the host's OnMessage callback.
func (p *InferenceProxy) HandleMessage(msg *sidecar.Message) {
	if p == nil || msg == nil {
		return
	}

	switch msg.Type {
	case sidecar.MsgOcclusionReady:
		p.handleOcclusionReady(msg)
	case sidecar.MsgARSlotDone:
		p.handleSlotDone(msg)
	}
}

// handleOcclusionReady imports IPC handles for mask and depth buffers.
func (p *InferenceProxy) handleOcclusionReady(msg *sidecar.Message) {
	data, ok := msg.Data.(map[string]any)
	if !ok {
		return
	}

	maskW := int(floatVal(data, "mask_w"))
	maskH := int(floatVal(data, "mask_h"))
	depthW := int(floatVal(data, "depth_w"))
	depthH := int(floatVal(data, "depth_h"))

	// Parse and import mask handle.
	var maskPtr uintptr
	if maskHandleStr, ok := data["mask_handle"].(string); ok && maskHandleStr != "" {
		mh, err := parseBase64Handle(maskHandleStr)
		if err != nil {
			slog.Error("arinfer proxy: parse mask handle", "error", err)
			return
		}
		ptr, err := sidecar.ImportIPCHandle(mh)
		if err != nil {
			slog.Error("arinfer proxy: import mask handle", "error", err)
			return
		}
		maskPtr = ptr
	}

	// Parse and import depth handle.
	var depthPtr uintptr
	if depthHandleStr, ok := data["depth_handle"].(string); ok && depthHandleStr != "" {
		dh, err := parseBase64Handle(depthHandleStr)
		if err != nil {
			slog.Error("arinfer proxy: parse depth handle", "error", err)
			if maskPtr != 0 {
				sidecar.CloseIPCHandle(maskPtr)
			}
			return
		}
		ptr, err := sidecar.ImportIPCHandle(dh)
		if err != nil {
			slog.Error("arinfer proxy: import depth handle", "error", err)
			if maskPtr != 0 {
				sidecar.CloseIPCHandle(maskPtr)
			}
			return
		}
		depthPtr = ptr
	}

	// Close old handles and store new ones.
	p.mu.Lock()
	if p.maskDevPtr != 0 {
		sidecar.CloseIPCHandle(p.maskDevPtr)
	}
	if p.depthDevPtr != 0 {
		sidecar.CloseIPCHandle(p.depthDevPtr)
	}
	p.maskDevPtr = maskPtr
	p.depthDevPtr = depthPtr
	p.maskW = maskW
	p.maskH = maskH
	p.depthW = depthW
	p.depthH = depthH
	p.mu.Unlock()

	// Update the atomic occlusion map.
	p.lastOcclusion.Store(&OcclusionMap{
		MaskPtr:  maskPtr,
		DepthPtr: depthPtr,
		Width:    maskW,
		Height:   maskH,
		DepthW:   depthW,
		DepthH:   depthH,
	})

	slog.Info("arinfer proxy: occlusion handles imported",
		"mask", [2]int{maskW, maskH}, "depth", [2]int{depthW, depthH})
}

// handleSlotDone releases an in-flight slot and its held frame reference.
func (p *InferenceProxy) handleSlotDone(msg *sidecar.Message) {
	data, ok := msg.Data.(map[string]any)
	if !ok {
		return
	}
	slot := int(floatVal(data, "slot"))
	if slot < 0 || (p.pool != nil && slot >= p.pool.SlotCount()) {
		return
	}

	// Release the in-flight frame reference.
	p.inFlightMu.Lock()
	frame := p.inFlight[slot]
	delete(p.inFlight, slot)
	p.inFlightMu.Unlock()

	if frame != nil {
		frame.Release()
	}

	if p.pool != nil {
		if ctrl := p.pool.Control(); ctrl != nil {
			ctrl.CompareAndSwapState(slot, sidecar.SlotDone, sidecar.SlotIdle)
		}
	}

	p.framesProcessed.Add(1)
}

// RecoverStaleSlots resets stuck slots after a sidecar crash,
// releasing any held frame references. The 5s watchdog interval is safe
// for AR inference (~20ms per frame). TRT engine builds (30-60s) happen
// in the command handler, not in slot processing, so they don't cause
// false positives here.
func (p *InferenceProxy) RecoverStaleSlots() {
	if p == nil || p.pool == nil {
		return
	}
	ctrl := p.pool.Control()
	slotCount := p.pool.SlotCount()

	for i := 0; i < slotCount; i++ {
		state := ctrl.LoadState(i)
		if state == sidecar.SlotDone || state == sidecar.SlotProcessing || state == sidecar.SlotReady {
			ctrl.CompareAndSwapState(i, state, sidecar.SlotIdle)

			p.inFlightMu.Lock()
			frame := p.inFlight[i]
			delete(p.inFlight, i)
			p.inFlightMu.Unlock()

			if frame != nil {
				frame.Release()
			}
		}
	}
	slog.Info("arinfer proxy: recovered stale slots after sidecar restart")
}

// ReenableState re-sends enable commands for depth/SAM/prompts to the sidecar
// after a restart. The sidecar loses all state on crash — this restores it.
func (p *InferenceProxy) ReenableState() {
	if p == nil || p.host == nil {
		return
	}

	// Close old imported handles (sidecar's buffers are gone).
	p.mu.Lock()
	if p.maskDevPtr != 0 {
		sidecar.CloseIPCHandle(p.maskDevPtr)
		p.maskDevPtr = 0
	}
	if p.depthDevPtr != 0 {
		sidecar.CloseIPCHandle(p.depthDevPtr)
		p.depthDevPtr = 0
	}
	depthEnabled := p.depthEnabled
	samEnabled := p.samEnabled
	prompts := make([]SAMPrompt, len(p.prompts))
	copy(prompts, p.prompts)
	p.mu.Unlock()

	p.lastOcclusion.Store(nil)

	if depthEnabled {
		if err := p.host.Send(&sidecar.Message{Cmd: sidecar.CmdEnableDepth}); err != nil {
			slog.Error("arinfer proxy: re-enable depth failed after restart", "error", err)
			p.mu.Lock()
			p.depthEnabled = false
			p.mu.Unlock()
		} else {
			slog.Info("arinfer proxy: re-enabled depth after restart")
		}
	}
	if samEnabled {
		if err := p.host.Send(&sidecar.Message{Cmd: sidecar.CmdEnableSAM}); err != nil {
			slog.Error("arinfer proxy: re-enable SAM failed after restart", "error", err)
			p.mu.Lock()
			p.samEnabled = false
			p.prompts = nil
			p.mu.Unlock()
		} else {
			slog.Info("arinfer proxy: re-enabled SAM after restart")
			for _, pr := range prompts {
				if err := p.host.Send(&sidecar.Message{
					Cmd:    sidecar.CmdAddPrompt,
					Config: pr,
				}); err != nil {
					slog.Warn("arinfer proxy: re-add prompt failed", "id", pr.ID, "error", err)
				}
			}
		}
	}
}

// Close releases all proxy resources, including any in-flight frame references.
func (p *InferenceProxy) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	if p.doneCh != nil {
		close(p.doneCh)
	}

	p.mu.Lock()
	if p.maskDevPtr != 0 {
		sidecar.CloseIPCHandle(p.maskDevPtr)
		p.maskDevPtr = 0
	}
	if p.depthDevPtr != 0 {
		sidecar.CloseIPCHandle(p.depthDevPtr)
		p.depthDevPtr = 0
	}
	p.mu.Unlock()

	// Release all in-flight frame references.
	p.inFlightMu.Lock()
	for slot, frame := range p.inFlight {
		if frame != nil {
			frame.Release()
		}
		delete(p.inFlight, slot)
	}
	p.inFlightMu.Unlock()

	p.lastOcclusion.Store(nil)
	slog.Info("arinfer proxy: closed")
}

// --- Helpers ---

func floatVal(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func parseBase64Handle(s string) (sidecar.CUDAIPCHandle, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return sidecar.CUDAIPCHandle{}, err
	}
	if len(b) != 64 {
		return sidecar.CUDAIPCHandle{}, fmt.Errorf("handle: decoded %d bytes, want 64", len(b))
	}
	var h sidecar.CUDAIPCHandle
	copy(h[:], b)
	return h, nil
}

// Compile-time check that InferenceProxy implements Inferrer.
var _ Inferrer = (*InferenceProxy)(nil)
