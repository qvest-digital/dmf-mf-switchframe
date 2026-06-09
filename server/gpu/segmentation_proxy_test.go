//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/zsiec/switchframe/server/sidecar"
)

func TestSegmentationProxy_ImplementsSegmenter(t *testing.T) {
	// Compile-time check.
	var _ Segmenter = (*SegmentationProxy)(nil)
}

func TestSegmentationProxy_PendingConfig(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	// Initially no pending config.
	if p.HasPendingConfig("cam1") {
		t.Error("expected HasPendingConfig=false initially")
	}
	if p.PendingSmoothing("cam1") != 0 {
		t.Error("expected PendingSmoothing=0 initially")
	}

	// Set pending config.
	p.SetPendingConfig("cam1", 0.7)
	if !p.HasPendingConfig("cam1") {
		t.Error("expected HasPendingConfig=true after set")
	}
	if s := p.PendingSmoothing("cam1"); s != 0.7 {
		t.Errorf("expected PendingSmoothing=0.7, got %f", s)
	}

	// Once a source is enabled, HasPendingConfig returns false.
	p.mu.Lock()
	p.sources["cam1"] = &proxySource{width: 1920, height: 1080}
	p.mu.Unlock()
	if p.HasPendingConfig("cam1") {
		t.Error("expected HasPendingConfig=false when source exists")
	}

	// Clear pending config.
	p.ClearPendingConfig("cam1")
	if p.PendingSmoothing("cam1") != 0 {
		t.Error("expected PendingSmoothing=0 after clear")
	}
}

func TestSegmentationProxy_IsEnabled(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	if p.IsEnabled("cam1") {
		t.Error("expected IsEnabled=false initially")
	}

	p.mu.Lock()
	p.sources["cam1"] = &proxySource{width: 1920, height: 1080}
	p.mu.Unlock()

	if !p.IsEnabled("cam1") {
		t.Error("expected IsEnabled=true after adding source")
	}
}

func TestSegmentationProxy_MaskForSource_NoMask(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	// No mask for unknown source.
	if m := p.MaskForSource("cam1"); m != nil {
		t.Error("expected nil mask for unknown source")
	}

	// Add source but no mask yet.
	p.mu.Lock()
	p.sources["cam1"] = &proxySource{width: 1920, height: 1080}
	p.mu.Unlock()

	if m := p.MaskForSource("cam1"); m != nil {
		t.Error("expected nil mask before mask_ready")
	}
}

func TestSegmentationProxy_HandleMaskReady(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	// Add source.
	p.mu.Lock()
	p.sources["cam1"] = &proxySource{width: 1920, height: 1080}
	p.mu.Unlock()

	// mask_ready for unknown source should be logged but not crash.
	p.HandleMessage(&sidecar.Message{
		Type: "mask_ready",
		Data: &sidecar.MaskHandleMessage{
			SourceID: "unknown",
			Handle:   sidecar.CUDAIPCHandle{},
			Width:    1920,
			Height:   1080,
		},
	})

	// Note: actual IPC handle import requires CUDA to be initialized,
	// which happens in the full integration test (Task 16).
}

func TestSegmentationProxy_HandleSlotDone(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	// Add a fake in-flight frame.
	fakeFrame := &GPUFrame{Width: 1920, Height: 1080}
	fakeFrame.refs.Store(2) // Ref'd by proxy + caller
	p.inFlightMu.Lock()
	p.inFlight[3] = fakeFrame
	p.inFlightMu.Unlock()

	// Simulate slot_done.
	p.HandleMessage(&sidecar.Message{
		Type: "slot_done",
		Data: map[string]any{"slot": float64(3)},
	})

	// Frame should be released (ref decremented) and removed from map.
	p.inFlightMu.Lock()
	_, exists := p.inFlight[3]
	p.inFlightMu.Unlock()
	if exists {
		t.Error("expected in-flight frame to be removed")
	}

	// Verify Release was called (refs should have decremented from 2 to 1).
	if refs := fakeFrame.refs.Load(); refs != 1 {
		t.Errorf("expected refs=1 after Release, got %d", refs)
	}
}

func TestSegmentationProxy_NilSafe(t *testing.T) {
	var p *SegmentationProxy

	// All methods should be safe on nil proxy.
	if err := p.EnableSource("cam1", 1920, 1080, 0.5); err == nil {
		t.Error("expected error from nil proxy EnableSource")
	}
	p.DisableSource("cam1")
	p.SetPendingConfig("cam1", 0.5)
	if p.HasPendingConfig("cam1") {
		t.Error("expected HasPendingConfig=false on nil")
	}
	if p.PendingSmoothing("cam1") != 0 {
		t.Error("expected PendingSmoothing=0 on nil")
	}
	p.ClearPendingConfig("cam1")
	if p.IsEnabled("cam1") {
		t.Error("expected IsEnabled=false on nil")
	}
	p.Segment("cam1", nil)
	if p.MaskForSource("cam1") != nil {
		t.Error("expected MaskForSource=nil on nil")
	}
	p.HandleMessage(nil)
	p.Close()
}

func TestSegmentationProxy_DevPtrToSlotMapping(t *testing.T) {
	// Verify the devPtrToSlot map is built correctly.
	// We can't use NewSegmentationProxy without a real pool,
	// but we can test the internal map logic.
	p := &SegmentationProxy{
		devPtrToSlot:   map[uintptr]int{0x1000: 0, 0x2000: 1, 0x3000: 2},
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	if slot, ok := p.devPtrToSlot[0x1000]; !ok || slot != 0 {
		t.Error("expected slot 0 for ptr 0x1000")
	}
	if slot, ok := p.devPtrToSlot[0x2000]; !ok || slot != 1 {
		t.Error("expected slot 1 for ptr 0x2000")
	}
	if _, ok := p.devPtrToSlot[0x9999]; ok {
		t.Error("expected no mapping for unknown ptr")
	}
}

func TestSegmentationProxy_PoolExhaustion(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	// Fill in-flight to the limit.
	for i := 0; i < maxInFlight; i++ {
		f := &GPUFrame{Width: 64, Height: 64}
		f.refs.Store(2)
		p.inFlight[i] = f
	}

	// Verify the constant is reasonable.
	if maxInFlight > 30 {
		t.Errorf("maxInFlight=%d is too high for a 32-slot pool", maxInFlight)
	}
	if maxInFlight < 16 {
		t.Errorf("maxInFlight=%d leaves too little capacity for inference", maxInFlight)
	}

	// In-flight count should be at the limit.
	if len(p.inFlight) != maxInFlight {
		t.Errorf("expected %d in-flight, got %d", maxInFlight, len(p.inFlight))
	}
}

// newTestMaskFrame builds a fake IPC-imported mask frame the same way
// handleMaskReady does: one owning reference held by the proxy and a release
// hook (standing in for sidecar.CloseIPCHandle) that runs when the last
// reference is released.
func newTestMaskFrame(closed *int) *GPUFrame {
	f := &GPUFrame{
		Width:     1920,
		Height:    1080,
		Pitch:     1920,
		releaseFn: func() { *closed++ },
	}
	f.refs.Store(1)
	return f
}

func TestSegmentationProxy_DeferredIPCClose_InFlightReader(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}

	closed := 0
	frame := newTestMaskFrame(&closed)
	src := &proxySource{width: 1920, height: 1080, maskDevPtr: 0xBEEF, maskFrame: frame}
	src.mask.Store(frame)
	p.sources["cam1"] = src

	// Pipeline takes a reference (kernel still reading the mask).
	m := p.MaskForSource("cam1")
	if m == nil {
		t.Fatal("expected mask frame")
	}

	// Teardown while the reader holds a ref must NOT close the IPC handle.
	p.Close()
	if closed != 0 {
		t.Fatalf("IPC handle closed while a reader still held a reference (closed=%d)", closed)
	}

	// The reader's Release drops the last reference and closes the handle.
	m.Release()
	if closed != 1 {
		t.Fatalf("expected IPC handle closed exactly once after last release, got %d", closed)
	}
}

func TestSegmentationProxy_CleanupSource_ClosesWhenIdle(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}
	defer p.Close()

	closed := 0
	frame := newTestMaskFrame(&closed)
	src := &proxySource{width: 1920, height: 1080, maskDevPtr: 0xBEEF, maskFrame: frame}
	src.mask.Store(frame)

	// No in-flight readers: cleanup closes the handle immediately, once.
	p.cleanupSource(src)
	if closed != 1 {
		t.Fatalf("expected IPC handle closed exactly once on idle cleanup, got %d", closed)
	}
	if src.maskFrame != nil || src.maskDevPtr != 0 || src.mask.Load() != nil {
		t.Error("expected mask state cleared after cleanup")
	}
}

func TestSegmentationProxy_Close_CleansUp(t *testing.T) {
	p := &SegmentationProxy{
		sources:        make(map[string]*proxySource),
		pendingConfigs: make(map[string]float32),
		inFlight:       make(map[int]*GPUFrame),
	}

	// Add some state.
	p.sources["cam1"] = &proxySource{width: 1920, height: 1080}
	p.pendingConfigs["cam2"] = 0.5

	// Add a fake in-flight frame.
	fakeFrame := &GPUFrame{Width: 1920, Height: 1080}
	fakeFrame.refs.Store(2)
	p.inFlight[0] = fakeFrame

	p.Close()

	p.mu.RLock()
	if len(p.sources) != 0 {
		t.Error("expected sources to be empty after close")
	}
	if len(p.pendingConfigs) != 0 {
		t.Error("expected pendingConfigs to be empty after close")
	}
	p.mu.RUnlock()

	p.inFlightMu.Lock()
	if len(p.inFlight) != 0 {
		t.Error("expected inFlight to be empty after close")
	}
	p.inFlightMu.Unlock()
}
