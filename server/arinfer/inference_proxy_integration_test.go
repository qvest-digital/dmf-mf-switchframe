//go:build cgo && cuda && integration

package arinfer

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/sidecar"
)

const aiSidecarBin = "/tmp/switchframe-ai"

// TestInferenceProxy_EndToEnd is an integration test that exercises the
// InferenceProxy with a real AI sidecar process and CUDA GPU:
//  1. Creates a GPU context and FramePool
//  2. Exports the FramePool's device pointers as a CUDAGPUPool
//  3. Spawns the AI sidecar and sends the GPU pool handshake
//  4. Creates an InferenceProxy wired to the sidecar
//  5. Enables depth estimation and waits for occlusion_ready
//  6. Submits a frame and waits for ar_slot_done
//  7. Verifies status counters, then disables depth
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestInferenceProxy_EndToEnd ./arinfer/
func TestInferenceProxy_EndToEnd(t *testing.T) {
	if _, err := os.Stat(aiSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", aiSidecarBin)
	}

	// Create GPU context and frame pool.
	gpuCtx, err := gpu.NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	defer gpuCtx.Close()

	pool, err := gpu.NewFramePool(gpuCtx, 640, 480, 8)
	if err != nil {
		t.Fatalf("create frame pool: %v", err)
	}
	defer pool.Close()

	// Create CUDA IPC pool from the FramePool's device pointers.
	framePtrs := pool.AllDevPtrs()
	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width: 640, Height: 480, Pitch: pool.Pitch(),
	}, "/switchframe-arproxy-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	// Spawn sidecar.
	var mu sync.Mutex
	var occlusionReceived bool
	var arSlotDone bool
	occlusionCh := make(chan struct{}, 1)
	arSlotDoneCh := make(chan struct{}, 1)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       aiSidecarBin,
		ReadyTimeout: 30 * time.Second,
	})
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	// Send handshake.
	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-arproxy-test",
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

	// Create InferenceProxy.
	proxy := NewInferenceProxy(host, ipcPool, pool)
	defer proxy.Close()

	// Wire OnMessage to dispatch to the proxy and track events.
	host.OnMessage = func(msg *sidecar.Message) {
		proxy.HandleMessage(msg)
		switch msg.Type {
		case sidecar.MsgOcclusionReady:
			mu.Lock()
			occlusionReceived = true
			mu.Unlock()
			select {
			case occlusionCh <- struct{}{}:
			default:
			}
		case sidecar.MsgARSlotDone:
			mu.Lock()
			arSlotDone = true
			mu.Unlock()
			select {
			case arSlotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	// Enable depth estimation.
	if err := proxy.EnableDepth(""); err != nil {
		t.Fatalf("enable depth: %v", err)
	}
	if !proxy.IsActive() {
		t.Error("expected IsActive=true after EnableDepth")
	}

	// Wait for occlusion_ready (depth handle).
	select {
	case <-occlusionCh:
		t.Log("occlusion_ready received for depth")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for occlusion_ready")
	}

	// Submit a frame via the proxy.
	frame, err := pool.Acquire()
	if err != nil {
		t.Fatalf("acquire frame: %v", err)
	}
	yuv := make([]byte, 640*480*3/2)
	for i := range yuv {
		yuv[i] = 128
	}
	if err := gpu.Upload(gpuCtx, frame, yuv, 640, 480); err != nil {
		frame.Release()
		t.Fatalf("upload: %v", err)
	}

	// Find the frame's device pointer as uintptr for ProcessFrame.
	// We cannot cast frame.DevPtr directly across cgo package boundaries,
	// so match against the FramePool's exported device pointers.
	allPtrs := pool.AllDevPtrs()
	allFrames := pool.AllFrames()
	var framePtr uintptr
	for i, f := range allFrames {
		if f == frame {
			framePtr = allPtrs[i]
			break
		}
	}
	if framePtr == 0 {
		frame.Release()
		t.Fatal("could not find frame DevPtr in pool")
	}

	proxy.ProcessFrame(framePtr, 640, 480, pool.Pitch())
	frame.Release() // proxy Ref'd internally

	// Wait for ar_slot_done.
	select {
	case <-arSlotDoneCh:
		t.Log("ar_slot_done received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ar_slot_done")
	}

	// Verify status.
	status := proxy.Status()
	if !status.DepthReady {
		t.Error("expected DepthReady=true")
	}
	if status.FramesProcessed < 1 {
		t.Errorf("expected FramesProcessed >= 1, got %d", status.FramesProcessed)
	}

	mu.Lock()
	gotOcclusion := occlusionReceived
	gotSlotDone := arSlotDone
	mu.Unlock()

	if !gotOcclusion {
		t.Error("occlusion_ready was not received")
	}
	if !gotSlotDone {
		t.Error("ar_slot_done was not received")
	}

	// Disable depth and verify proxy deactivates.
	proxy.DisableDepth()
	if proxy.IsActive() {
		t.Error("expected IsActive=false after DisableDepth")
	}

	t.Log("inference proxy integration test passed")
}
