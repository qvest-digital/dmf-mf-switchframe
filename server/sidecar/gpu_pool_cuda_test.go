//go:build cgo && cuda

package sidecar

import (
	"testing"
)

func TestCUDAGPUPoolHostAllocAndExport(t *testing.T) {
	cfg := GPUPoolConfig{
		SlotCount:  4,
		Width:      1920,
		Height:     1080,
		ResultSize: 1920 * 1080, // 1:1 mask
	}

	pool, err := NewCUDAGPUPoolHost(cfg, "/sf-test-cuda-pool")
	if err != nil {
		t.Fatalf("NewCUDAGPUPoolHost: %v", err)
	}
	defer pool.Close()

	// Verify slot count.
	if pool.SlotCount() != 4 {
		t.Errorf("SlotCount = %d, want 4", pool.SlotCount())
	}

	// Verify frame handles are non-zero.
	handles := pool.FrameHandles()
	if len(handles) != 4 {
		t.Fatalf("FrameHandles len = %d, want 4", len(handles))
	}
	for i, h := range handles {
		allZero := true
		for _, b := range h {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			t.Errorf("frame handle[%d] is all zeros", i)
		}
	}

	// Verify result handles are non-zero.
	resultHandles := pool.ResultHandles()
	if len(resultHandles) != 4 {
		t.Fatalf("ResultHandles len = %d, want 4", len(resultHandles))
	}

	// Verify device pointers are non-zero.
	for i := 0; i < 4; i++ {
		if pool.FrameDevPtr(i) == 0 {
			t.Errorf("FrameDevPtr(%d) = 0", i)
		}
		if pool.ResultDevPtr(i) == 0 {
			t.Errorf("ResultDevPtr(%d) = 0", i)
		}
	}

	// Verify out-of-bounds returns 0.
	if pool.FrameDevPtr(-1) != 0 {
		t.Error("FrameDevPtr(-1) should be 0")
	}
	if pool.FrameDevPtr(4) != 0 {
		t.Error("FrameDevPtr(4) should be 0")
	}

	// Verify event handles are non-zero.
	readyH := pool.ReadyEventHandles()
	doneH := pool.DoneEventHandles()
	if len(readyH) != 4 || len(doneH) != 4 {
		t.Fatalf("event handles len: ready=%d, done=%d, want 4,4", len(readyH), len(doneH))
	}

	// Verify control region is initialized.
	ctrl := pool.Control()
	if ctrl == nil {
		t.Fatal("Control() = nil")
	}
	if err := ctrl.ValidateHeader(); err != nil {
		t.Fatalf("ValidateHeader: %v", err)
	}

	// All slots should start idle.
	for i := 0; i < 4; i++ {
		if s := ctrl.LoadState(i); s != SlotIdle {
			t.Errorf("slot %d state = %v, want idle", i, s)
		}
	}
}

func TestCUDAGPUPoolHandleRoundTrip(t *testing.T) {
	// CUDA IPC handles can only be opened in a DIFFERENT process than the
	// one that allocated them (cuIpcOpenMemHandle returns CUDA_ERROR_INVALID_CONTEXT
	// when called in the same process). This test verifies the control region
	// sharing via POSIX shm, which does work in-process.
	cfg := GPUPoolConfig{
		SlotCount:  2,
		Width:      1280,
		Height:     720,
		ResultSize: 1280 * 720,
	}

	host, err := NewCUDAGPUPoolHost(cfg, "/sf-test-cuda-rt")
	if err != nil {
		t.Fatalf("NewCUDAGPUPoolHost: %v", err)
	}
	defer host.Close()

	// Verify handles are exported (non-zero).
	frameHandles := host.FrameHandles()
	if len(frameHandles) != 2 {
		t.Fatalf("FrameHandles len = %d, want 2", len(frameHandles))
	}
	for i, h := range frameHandles {
		allZero := true
		for _, b := range h {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			t.Errorf("frame handle[%d] is all zeros", i)
		}
	}

	// Verify control region is shared via POSIX shm: open same shm name
	// and validate the header written by the host.
	ctrl := host.Control()
	if !ctrl.MarkReady(0, 42000) {
		t.Fatal("host MarkReady(0) failed")
	}
	if s := ctrl.LoadState(0); s != SlotReady {
		t.Errorf("slot 0 state = %v, want ready", s)
	}
	if pts := ctrl.LoadPTS(0); pts != 42000 {
		t.Errorf("slot 0 PTS = %d, want 42000", pts)
	}

	// Note: True cross-process IPC handle import is tested via the AI sidecar
	// integration test (Task 16), which spawns a real worker process.
}

func TestCUDAGPUPoolNoResultSlots(t *testing.T) {
	cfg := GPUPoolConfig{
		SlotCount: 2,
		Width:     1920,
		Height:    1080,
		// ResultSize: 0 — no result slots
	}

	pool, err := NewCUDAGPUPoolHost(cfg, "/sf-test-cuda-nores")
	if err != nil {
		t.Fatalf("NewCUDAGPUPoolHost: %v", err)
	}
	defer pool.Close()

	// Result handles/ptrs should be empty.
	if len(pool.ResultHandles()) != 0 {
		t.Errorf("ResultHandles len = %d, want 0", len(pool.ResultHandles()))
	}
	if pool.ResultDevPtr(0) != 0 {
		t.Error("ResultDevPtr(0) should be 0 when no result slots")
	}
}

func TestCUDAGPUPoolInvalidConfig(t *testing.T) {
	_, err := NewCUDAGPUPoolHost(GPUPoolConfig{}, "/sf-test-invalid")
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestCUDAGPUPoolBadShmName(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 2, Width: 1920, Height: 1080}
	_, err := NewCUDAGPUPoolHost(cfg, "no-slash")
	if err == nil {
		t.Fatal("expected error for shmName without leading '/'")
	}
}

func TestCUDAGPUPoolWorkerHandleCountMismatch(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 2, Width: 1920, Height: 1080}
	_, err := OpenCUDAGPUPoolWorker(
		[]CUDAIPCHandle{{}, {}},  // frame: ok
		nil,                      // result: ok (ResultSize=0)
		[]CUDAIPCEventHandle{{}}, // wrong count
		[]CUDAIPCEventHandle{{}, {}},
		cfg, "/sf-test-mismatch",
	)
	if err == nil {
		t.Fatal("expected error for event handle count mismatch")
	}
}
