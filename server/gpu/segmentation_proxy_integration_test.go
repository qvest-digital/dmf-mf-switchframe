//go:build cgo && cuda && integration

package gpu

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/sidecar"
)

const aiSidecarBin = "/tmp/switchframe-ai"

// TestSegmentationProxy_EndToEnd exercises the full SegmentationProxy lifecycle
// with a real AI sidecar process:
//
//  1. Create GPU context and frame pool (real CUDA allocations)
//  2. Create CUDA IPC pool from the frame pool's device pointers (zero-copy export)
//  3. Spawn AI sidecar binary via sidecar.Host
//  4. Send GPU pool handshake (IPC handles + shared memory control region)
//  5. Create SegmentationProxy and wire message handling
//  6. Enable segmentation for a source and wait for mask_ready
//  7. Upload a test frame, call Segment(), wait for slot_done
//  8. Verify MaskForSource returns a valid GPUFrame with correct dimensions
//  9. Disable segmentation and verify cleanup
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestSegmentationProxy_EndToEnd ./gpu/
func TestSegmentationProxy_EndToEnd(t *testing.T) {
	if _, err := os.Stat(aiSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", aiSidecarBin)
	}

	// --- Step 1: Create GPU context and frame pool ---
	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 640, 480, 8)
	if err != nil {
		t.Fatalf("create frame pool: %v", err)
	}
	defer pool.Close()

	// --- Step 2: Create CUDA IPC pool from frame pool device pointers ---
	framePtrs := pool.AllDevPtrs()
	if len(framePtrs) == 0 {
		t.Fatal("no frame pool device pointers")
	}

	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  640,
		Height: 480,
		Pitch:  pool.Pitch(),
	}, "/switchframe-segproxy-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	// --- Step 3: Spawn AI sidecar ---
	var mu sync.Mutex
	maskReadyCh := make(chan struct{}, 1)
	slotDoneCh := make(chan struct{}, 1)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       aiSidecarBin,
		ReadyTimeout: 30 * time.Second,
	})

	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	// --- Step 4: Send GPU pool handshake ---
	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-segproxy-test",
		FrameHandles: ipcPool.FrameHandles(),
		ReadyEvents:  ipcPool.ReadyEventHandles(),
		DoneEvents:   ipcPool.DoneEventHandles(),
	}

	conn := host.Conn()
	if conn == nil {
		t.Fatal("no connection to sidecar")
	}
	if err := sidecar.WriteGPUPoolHandshake(conn, hs); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// --- Step 5: Create SegmentationProxy and wire message handling ---
	proxy := NewSegmentationProxy(host, ipcPool)
	defer proxy.Close()

	// Wire OnMessage: proxy handles mask_ready/slot_done, we also signal channels.
	host.OnMessage = func(msg *sidecar.Message) {
		proxy.HandleMessage(msg)

		switch msg.Type {
		case sidecar.MsgMaskReady:
			select {
			case maskReadyCh <- struct{}{}:
			default:
			}
		case sidecar.MsgSlotDone:
			select {
			case slotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	// --- Step 6: Enable segmentation and wait for mask_ready ---
	if err := proxy.EnableSource("cam1", 640, 480, 0.5); err != nil {
		t.Fatalf("enable source: %v", err)
	}

	select {
	case <-maskReadyCh:
		t.Log("mask_ready received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for mask_ready")
	}

	// Verify proxy has source enabled.
	if !proxy.IsEnabled("cam1") {
		t.Error("expected cam1 to be enabled")
	}

	// --- Step 7: Upload a test frame and call Segment ---
	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame: %v", err)
	}

	// Create a gray YUV420 test pattern.
	yuv := make([]byte, 640*480*3/2)
	for i := 0; i < 640*480; i++ {
		yuv[i] = 128 // gray Y
	}
	for i := 640 * 480; i < len(yuv); i++ {
		yuv[i] = 128 // neutral chroma
	}
	if err := Upload(ctx, frame, yuv, 640, 480); err != nil {
		frame.Release()
		t.Fatalf("upload: %v", err)
	}

	frame.PTS = 90000 // 1 second at 90kHz
	proxy.Segment("cam1", frame)
	frame.Release() // proxy Ref'd it internally

	// --- Step 8: Wait for slot_done ---
	select {
	case <-slotDoneCh:
		t.Log("slot_done received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for slot_done")
	}

	// --- Step 9: Verify MaskForSource returns a valid frame ---
	mu.Lock()
	mask := proxy.MaskForSource("cam1")
	mu.Unlock()

	if mask == nil {
		t.Fatal("MaskForSource returned nil after mask_ready")
	}
	t.Logf("mask: %dx%d, DevPtr=0x%x", mask.Width, mask.Height, mask.DevPtr)

	if mask.Width != 640 || mask.Height != 480 {
		t.Errorf("mask dimensions %dx%d, want 640x480", mask.Width, mask.Height)
	}
	if mask.DevPtr == 0 {
		t.Error("mask DevPtr is zero")
	}

	// --- Step 10: Disable segmentation and verify cleanup ---
	proxy.DisableSource("cam1")
	if proxy.IsEnabled("cam1") {
		t.Error("expected cam1 disabled after DisableSource")
	}

	// MaskForSource should return nil after disable.
	if m := proxy.MaskForSource("cam1"); m != nil {
		t.Error("expected MaskForSource=nil after DisableSource")
	}

	t.Log("segmentation proxy end-to-end integration test passed")
}

// TestSegmentationProxy_MultiSource verifies the proxy can handle multiple
// concurrent segmentation sources, each with independent mask lifecycle.
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestSegmentationProxy_MultiSource ./gpu/
func TestSegmentationProxy_MultiSource(t *testing.T) {
	if _, err := os.Stat(aiSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", aiSidecarBin)
	}

	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 320, 240, 8)
	if err != nil {
		t.Fatalf("create frame pool: %v", err)
	}
	defer pool.Close()

	framePtrs := pool.AllDevPtrs()
	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width:  320,
		Height: 240,
		Pitch:  pool.Pitch(),
	}, "/switchframe-segproxy-multi")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	// Track mask_ready per source.
	var mu sync.Mutex
	maskReadySources := make(map[string]bool)
	maskReadyCh := make(chan string, 4)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       aiSidecarBin,
		ReadyTimeout: 30 * time.Second,
	})

	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	conn := host.Conn()
	if conn == nil {
		t.Fatal("no connection to sidecar")
	}

	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-segproxy-multi",
		FrameHandles: ipcPool.FrameHandles(),
		ReadyEvents:  ipcPool.ReadyEventHandles(),
		DoneEvents:   ipcPool.DoneEventHandles(),
	}
	if err := sidecar.WriteGPUPoolHandshake(conn, hs); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	proxy := NewSegmentationProxy(host, ipcPool)
	defer proxy.Close()

	host.OnMessage = func(msg *sidecar.Message) {
		proxy.HandleMessage(msg)

		if msg.Type == sidecar.MsgMaskReady {
			mh, err := sidecar.ParseMaskHandleMessage(msg)
			if err != nil || mh == nil {
				return
			}
			mu.Lock()
			maskReadySources[mh.SourceID] = true
			mu.Unlock()
			select {
			case maskReadyCh <- mh.SourceID:
			default:
			}
		}
	}

	// Enable two sources.
	if err := proxy.EnableSource("cam1", 320, 240, 0.3); err != nil {
		t.Fatalf("enable cam1: %v", err)
	}
	if err := proxy.EnableSource("cam2", 320, 240, 0.6); err != nil {
		t.Fatalf("enable cam2: %v", err)
	}

	// Wait for mask_ready for both sources.
	timeout := time.After(10 * time.Second)
	received := make(map[string]bool)
	for len(received) < 2 {
		select {
		case src := <-maskReadyCh:
			received[src] = true
			t.Logf("mask_ready received for %s", src)
		case <-timeout:
			t.Fatalf("timeout waiting for mask_ready (received: %v)", received)
		}
	}

	// Verify both are enabled.
	if !proxy.IsEnabled("cam1") {
		t.Error("cam1 not enabled")
	}
	if !proxy.IsEnabled("cam2") {
		t.Error("cam2 not enabled")
	}

	// Verify masks are independent.
	mask1 := proxy.MaskForSource("cam1")
	mask2 := proxy.MaskForSource("cam2")
	if mask1 == nil {
		t.Fatal("cam1 mask is nil")
	}
	if mask2 == nil {
		t.Fatal("cam2 mask is nil")
	}
	if mask1.DevPtr == mask2.DevPtr {
		t.Error("cam1 and cam2 should have different mask DevPtrs")
	}

	// Disable cam1, cam2 should be unaffected.
	proxy.DisableSource("cam1")
	if proxy.IsEnabled("cam1") {
		t.Error("cam1 should be disabled")
	}
	if !proxy.IsEnabled("cam2") {
		t.Error("cam2 should still be enabled")
	}
	if proxy.MaskForSource("cam1") != nil {
		t.Error("cam1 mask should be nil after disable")
	}
	if proxy.MaskForSource("cam2") == nil {
		t.Error("cam2 mask should still exist")
	}

	t.Log("multi-source segmentation proxy test passed")
}
