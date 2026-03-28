//go:build cgo && cuda

package tracking

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

// maxInFlight limits how many GPU pool frames can be Ref'd for sidecar
// visual tracking at once. With a 32-slot FramePool, this preserves 8 frames
// of pipeline headroom to avoid cudaMallocPitch on the hot path.
const maxInFlight = 24

// VisualTrackingProxy implements TrackingSource by delegating SuperPoint +
// LightGlue visual tracking to the AI sidecar. Input frames are shared via
// the CUDA IPC GPU pool. Output poses are sent as JSON over the control
// socket (small enough that IPC memory sharing is unnecessary).
type VisualTrackingProxy struct {
	host      *sidecar.Host
	pool      *sidecar.CUDAGPUPool
	sourceKey string
	name      string

	devPtrToSlot map[uintptr]int

	// FramePool DevPtr → GPUFrame. Built once at construction from the
	// FramePool's pre-allocated frames. Used to Ref/Release frames while
	// the sidecar reads them asynchronously, preventing the FramePool from
	// recycling VRAM out from under the sidecar.
	devPtrToFrame map[uintptr]*gpu.GPUFrame

	mu      sync.Mutex
	running bool

	// In-flight frame tracking: slot → Ref'd GPUFrame. Prevents the
	// FramePool from recycling memory while the sidecar reads it.
	inFlightMu sync.Mutex
	inFlight   map[int]*gpu.GPUFrame

	lastPose atomic.Pointer[CameraPose]

	doneCh chan struct{}
	closed atomic.Bool
}

// NewVisualTrackingProxy creates a proxy that delegates visual tracking
// to the AI sidecar. sourceKey identifies the camera source.
//
// The framePool parameter provides access to the pre-allocated GPUFrame objects.
// The proxy builds a DevPtr→GPUFrame map so it can Ref frames before handing
// them to the sidecar and Release them when processing completes, preventing
// the FramePool from recycling VRAM while the sidecar reads it.
func NewVisualTrackingProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool, sourceKey string, framePool *gpu.FramePool) *VisualTrackingProxy {
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

	p := &VisualTrackingProxy{
		host:          host,
		pool:          pool,
		sourceKey:     sourceKey,
		name:          "visual-sidecar-" + sourceKey,
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
func (p *VisualTrackingProxy) startWatchdog() {
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

func (p *VisualTrackingProxy) Name() string { return p.name }
func (p *VisualTrackingProxy) Type() string { return "visual" }

// Start sends enable_tracking to the sidecar.
func (p *VisualTrackingProxy) Start() error {
	if p == nil || p.closed.Load() {
		return fmt.Errorf("tracking proxy: not available")
	}

	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	// Set running optimistically before Send to prevent concurrent Start().
	p.running = true
	p.mu.Unlock()

	err := p.host.Send(&sidecar.Message{
		Cmd: sidecar.CmdEnableTracking,
		Config: map[string]any{
			"source_id": p.sourceKey,
		},
	})
	if err != nil {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		return fmt.Errorf("tracking proxy: start: %w", err)
	}

	slog.Info("tracking proxy: started", "source", p.sourceKey)
	return nil
}

// Stop sends disable_tracking to the sidecar.
func (p *VisualTrackingProxy) Stop() error {
	if p == nil {
		return nil
	}

	_ = p.host.Send(&sidecar.Message{
		Cmd: sidecar.CmdDisableTracking,
		Config: map[string]any{
			"source_id": p.sourceKey,
		},
	})

	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	slog.Info("tracking proxy: stopped", "source", p.sourceKey)
	return nil
}

// Running reports whether the tracker is active.
func (p *VisualTrackingProxy) Running() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// LatestPose returns the most recent camera pose from the sidecar.
func (p *VisualTrackingProxy) LatestPose() *CameraPose {
	if p == nil {
		return nil
	}
	return p.lastPose.Load()
}

// ProcessFrame submits a frame for visual tracking in the sidecar.
// Called from the source manager or tracking poll loop.
func (p *VisualTrackingProxy) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if p == nil || p.closed.Load() {
		return
	}

	p.mu.Lock()
	running := p.running
	p.mu.Unlock()
	if !running {
		return
	}

	slot, ok := p.devPtrToSlot[framePtr]
	if !ok {
		return
	}

	if p.pool == nil {
		return
	}
	ctrl := p.pool.Control()
	if ctrl == nil {
		return
	}

	// Write source key to shared memory BEFORE CAS. Prefix with "__track:"
	// so the sidecar frameLoop can dispatch tracking vs segmentation.
	ctrl.SetSourceKey(slot, sidecar.SourceKeyTrackPrefix+p.sourceKey)

	if !ctrl.CompareAndSwapState(slot, sidecar.SlotIdle, sidecar.SlotReady) {
		return // slot busy
	}

	// Check pool exhaustion before Ref'ing the frame.
	p.inFlightMu.Lock()
	inFlightCount := len(p.inFlight)
	p.inFlightMu.Unlock()

	if inFlightCount >= maxInFlight {
		// Revert: mark slot back to Idle since we can't hold more frames.
		ctrl.CompareAndSwapState(slot, sidecar.SlotReady, sidecar.SlotIdle)
		slog.Warn("tracking proxy: pool exhaustion, skipping frame",
			"source", p.sourceKey, "in_flight", inFlightCount, "max", maxInFlight)
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

	ctrl.SetPTS(slot, 0)
}

// HandleMessage processes messages from the AI sidecar.
// Must be called from the host's OnMessage callback.
func (p *VisualTrackingProxy) HandleMessage(msg *sidecar.Message) {
	if p == nil || msg == nil {
		return
	}

	switch msg.Type {
	case sidecar.MsgPoseResult:
		p.handlePoseResult(msg)
	case sidecar.MsgTrackSlotDone:
		p.handleSlotDone(msg)
	}
}

// handlePoseResult receives a CameraPose from the sidecar.
func (p *VisualTrackingProxy) handlePoseResult(msg *sidecar.Message) {
	raw, err := json.Marshal(msg.Data)
	if err != nil {
		slog.Warn("tracking proxy: marshal pose data", "error", err)
		return
	}
	var pose CameraPose
	if err := json.Unmarshal(raw, &pose); err != nil {
		slog.Warn("tracking proxy: unmarshal pose", "error", err)
		return
	}
	pose.Source = "visual"
	p.lastPose.Store(&pose)
}

// handleSlotDone marks a GPU pool slot as idle after the sidecar finishes,
// releasing the held frame reference.
func (p *VisualTrackingProxy) handleSlotDone(msg *sidecar.Message) {
	data, ok := msg.Data.(map[string]any)
	if !ok {
		return
	}
	slotF, _ := data["slot"].(float64)
	slot := int(slotF)
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
}

// RecoverStaleSlots resets stuck slots after a sidecar crash,
// releasing any held frame references.
func (p *VisualTrackingProxy) RecoverStaleSlots() {
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
	slog.Info("tracking proxy: recovered stale slots after sidecar restart")
}

// Close releases resources and stops the tracker, including any in-flight
// frame references.
func (p *VisualTrackingProxy) Close() error {
	if p == nil {
		return nil
	}
	p.closed.Store(true)
	if p.doneCh != nil {
		close(p.doneCh)
	}
	_ = p.Stop()

	// Release all in-flight frame references.
	p.inFlightMu.Lock()
	for slot, frame := range p.inFlight {
		if frame != nil {
			frame.Release()
		}
		delete(p.inFlight, slot)
	}
	p.inFlightMu.Unlock()

	p.lastPose.Store(nil)
	slog.Info("tracking proxy: closed", "source", p.sourceKey)
	return nil
}

// Compile-time check.
var _ TrackingSource = (*VisualTrackingProxy)(nil)
