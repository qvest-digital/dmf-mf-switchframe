//go:build cgo && cuda

package sidecar

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// gpuWorkerBin is set by init() if the gpuworker binary can be built.
var (
	gpuWorkerBin  string
	gpuWorkerOnce sync.Once
)

func buildGPUWorker(t *testing.T) string {
	t.Helper()
	gpuWorkerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gpu-worker-test-*")
		if err != nil {
			return
		}
		bin := filepath.Join(dir, "gpuworker")
		cmd := exec.Command("go", "build", "-tags", "cgo,cuda", "-o", bin, "./testdata/gpuworker")
		cmd.Dir = "."
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return
		}
		gpuWorkerBin = bin
	})
	if gpuWorkerBin == "" {
		t.Skip("could not build gpuworker binary")
	}
	return gpuWorkerBin
}

func TestCUDAGPUPoolCrossProcessIPC(t *testing.T) {
	bin := buildGPUWorker(t)

	// Allocate a GPU pool on the host side.
	cfg := GPUPoolConfig{
		SlotCount:  2,
		Width:      1920,
		Height:     1080,
		ResultSize: 1920 * 1080, // 1:1 uint8 mask
	}
	shmName := "/sf-test-xproc-ipc"

	pool, err := NewCUDAGPUPoolHost(cfg, shmName)
	if err != nil {
		t.Fatalf("NewCUDAGPUPoolHost: %v", err)
	}
	defer pool.Close()

	// Mark slot 0 as ready with a known PTS — worker will observe this.
	ctrl := pool.Control()
	ctrl.MarkReady(0, 99000)

	// Build the handshake.
	hs := &GPUPoolHandshake{
		Config:        cfg,
		ShmName:       shmName,
		FrameHandles:  pool.FrameHandles(),
		ResultHandles: pool.ResultHandles(),
		ReadyEvents:   pool.ReadyEventHandles(),
		DoneEvents:    pool.DoneEventHandles(),
	}

	// Spawn the worker using the SDK Host.
	var (
		mu      sync.Mutex
		gotResp *Message
		respCh  = make(chan struct{}, 1)
	)

	h := NewHost(Config{
		Binary:          bin,
		ReadyTimeout:    10 * time.Second,
		DisableReadLoop: true,
	})
	// We'll read manually since we need to send the handshake first.

	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("Host.Start: %v", err)
	}
	defer h.Close()

	// Send the GPU pool handshake to the worker.
	conn := h.Conn()
	if conn == nil {
		t.Fatal("no connection to worker")
	}
	if err := WriteGPUPoolHandshake(conn, hs); err != nil {
		t.Fatalf("WriteGPUPoolHandshake: %v", err)
	}

	// Read the worker's response.
	go func() {
		msg, err := ReadMessage(conn)
		if err != nil {
			return
		}
		mu.Lock()
		gotResp = msg
		mu.Unlock()
		select {
		case respCh <- struct{}{}:
		default:
		}
	}()

	select {
	case <-respCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for worker response")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotResp == nil {
		t.Fatal("no response from worker")
	}

	if gotResp.Type == "error" {
		t.Fatalf("worker error: %s", gotResp.Error)
	}

	if gotResp.Type != "status" {
		t.Fatalf("unexpected response type: %q", gotResp.Type)
	}

	// Parse the worker's status data.
	data, ok := gotResp.Data.(map[string]any)
	if !ok {
		t.Fatalf("response data type = %T, want map", gotResp.Data)
	}

	// Worker should have seen slot 0 in "ready" state.
	if s, _ := data["slot0_state"].(string); s != "ready" {
		t.Errorf("worker saw slot0_state = %q, want 'ready'", s)
	}

	// Worker should have seen PTS 99000.
	if pts, _ := data["slot0_pts"].(float64); int64(pts) != 99000 {
		t.Errorf("worker saw slot0_pts = %v, want 99000", pts)
	}

	// Worker should have seen 2 slots.
	if sc, _ := data["slot_count"].(float64); int(sc) != 2 {
		t.Errorf("worker saw slot_count = %v, want 2", sc)
	}

	// The worker marked slot 0 as Processing → Done.
	// Verify host sees Done state.
	state := ctrl.LoadState(0)
	if state != SlotDone {
		t.Errorf("host sees slot 0 = %v after worker, want done", state)
	}
}
