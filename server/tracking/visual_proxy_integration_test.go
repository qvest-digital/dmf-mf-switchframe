//go:build cgo && cuda && integration

package tracking

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

// TestVisualTrackingProxy_EndToEnd is an integration test that exercises the
// VisualTrackingProxy with a real AI sidecar process and CUDA GPU:
//  1. Creates a GPU context and FramePool
//  2. Exports the FramePool's device pointers as a CUDAGPUPool
//  3. Spawns the AI sidecar and sends the GPU pool handshake
//  4. Creates a VisualTrackingProxy for "cam1"
//  5. Starts tracking and submits a frame
//  6. Waits for track_slot_done
//  7. Checks LatestPose (nil expected without real TRT model)
//  8. Stops tracking and verifies clean shutdown
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestVisualTrackingProxy_EndToEnd ./tracking/
func TestVisualTrackingProxy_EndToEnd(t *testing.T) {
	if _, err := os.Stat(aiSidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", aiSidecarBin)
	}

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

	framePtrs := pool.AllDevPtrs()
	ipcPool, err := sidecar.NewCUDAGPUPoolHostFromPtrs(framePtrs, sidecar.GPUPoolConfig{
		Width: 640, Height: 480, Pitch: pool.Pitch(),
	}, "/switchframe-trackproxy-test")
	if err != nil {
		t.Fatalf("create IPC pool: %v", err)
	}
	defer ipcPool.Close()

	var mu sync.Mutex
	var trackSlotDone bool
	trackSlotDoneCh := make(chan struct{}, 1)

	host := sidecar.NewHost(sidecar.Config{
		Binary:       aiSidecarBin,
		ReadyTimeout: 30 * time.Second,
	})
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	hs := &sidecar.GPUPoolHandshake{
		Config:       ipcPool.Config(),
		ShmName:      "/switchframe-trackproxy-test",
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

	// Create tracking proxy.
	proxy := NewVisualTrackingProxy(host, ipcPool, "cam1", pool)
	defer proxy.Close()

	// Wire OnMessage to dispatch to the proxy and track events.
	host.OnMessage = func(msg *sidecar.Message) {
		proxy.HandleMessage(msg)
		if msg.Type == sidecar.MsgTrackSlotDone {
			mu.Lock()
			trackSlotDone = true
			mu.Unlock()
			select {
			case trackSlotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	// Start tracking.
	if err := proxy.Start(); err != nil {
		t.Fatalf("start tracking: %v", err)
	}
	if !proxy.Running() {
		t.Error("expected Running=true after Start")
	}

	// Submit a frame.
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

	// Find DevPtr as uintptr by matching against FramePool's exported pointers.
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

	// Wait for track_slot_done.
	select {
	case <-trackSlotDoneCh:
		t.Log("track_slot_done received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for track_slot_done")
	}

	// Pose will be nil (no real TRT model) but should not crash.
	pose := proxy.LatestPose()
	t.Logf("latest pose: %v (nil expected without TRT model)", pose)

	mu.Lock()
	gotSlotDone := trackSlotDone
	mu.Unlock()
	if !gotSlotDone {
		t.Error("track_slot_done was not received")
	}

	// Stop tracking.
	if err := proxy.Stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
	if proxy.Running() {
		t.Error("expected Running=false after Stop")
	}

	t.Log("visual tracking proxy integration test passed")
}
