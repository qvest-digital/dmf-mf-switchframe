package sidecar

import (
	"testing"
)

func TestCUDAIPCHandleSize(t *testing.T) {
	// CUDAIPCHandle must be exactly 64 bytes (CU_IPC_HANDLE_SIZE).
	var h CUDAIPCHandle
	if len(h) != 64 {
		t.Errorf("CUDAIPCHandle size = %d, want 64", len(h))
	}

	var eh CUDAIPCEventHandle
	if len(eh) != 64 {
		t.Errorf("CUDAIPCEventHandle size = %d, want 64", len(eh))
	}
}

func TestCUDAIPCHandleCopy(t *testing.T) {
	// Verify handles are value types (copyable, comparable).
	var h1 CUDAIPCHandle
	for i := range h1 {
		h1[i] = byte(i)
	}

	h2 := h1
	if h1 != h2 {
		t.Error("copied handle should equal original")
	}

	h2[0] = 0xFF
	if h1 == h2 {
		t.Error("modified copy should not equal original")
	}
}

func TestCUDAIPCHandleSliceSerialization(t *testing.T) {
	// Simulate serialization: a slice of handles can be sent as raw bytes.
	handles := make([]CUDAIPCHandle, 4)
	for i := range handles {
		for j := range handles[i] {
			handles[i][j] = byte(i*64 + j)
		}
	}

	// Marshal: flatten to []byte.
	buf := make([]byte, len(handles)*64)
	for i, h := range handles {
		copy(buf[i*64:(i+1)*64], h[:])
	}

	// Unmarshal: reconstruct from []byte.
	restored := make([]CUDAIPCHandle, 4)
	for i := range restored {
		copy(restored[i][:], buf[i*64:(i+1)*64])
	}

	for i := range handles {
		if handles[i] != restored[i] {
			t.Errorf("handle[%d] round-trip mismatch", i)
		}
	}
}

func TestCUDAGPUPoolStubReturnsError(t *testing.T) {
	// On non-CUDA builds, NewCUDAGPUPoolHost should return an error.
	cfg := GPUPoolConfig{
		SlotCount: 4,
		Width:     1920,
		Height:    1080,
	}

	pool, err := NewCUDAGPUPoolHost(cfg, "/sf-test-stub")
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		// On CUDA builds, this is expected to succeed — skip the test.
		t.Skip("CUDA available, skipping stub test")
	}

	if pool != nil {
		t.Error("expected nil pool on error")
	}
}

func TestCUDAGPUPoolStubWorkerReturnsError(t *testing.T) {
	cfg := GPUPoolConfig{
		SlotCount: 2,
		Width:     1920,
		Height:    1080,
	}

	pool, err := OpenCUDAGPUPoolWorker(
		make([]CUDAIPCHandle, 2),
		nil,
		make([]CUDAIPCEventHandle, 2),
		make([]CUDAIPCEventHandle, 2),
		cfg,
		"/sf-test-stub-worker",
	)
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Skip("CUDA available, skipping stub test")
	}

	if pool != nil {
		t.Error("expected nil pool on error")
	}
}

func TestCUDAGPUPoolStubAccessors(t *testing.T) {
	// Verify stub pool accessors don't panic on a nil pool.
	var pool *CUDAGPUPool
	if pool.Close() != nil {
		t.Error("Close on nil pool should return nil")
	}
}

func TestCUDAGPUPoolZeroValueAccessors(t *testing.T) {
	// A zero-value pool (no constructor called) should not panic on accessors.
	pool := &CUDAGPUPool{
		cfg: GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080},
	}

	// DevPtr on a pool with no allocations should be 0.
	if ptr := pool.FrameDevPtr(0); ptr != 0 {
		t.Errorf("FrameDevPtr(0) = %d, want 0", ptr)
	}
	if ptr := pool.ResultDevPtr(0); ptr != 0 {
		t.Errorf("ResultDevPtr(0) = %d, want 0", ptr)
	}

	// Config should reflect what was set.
	if cfg := pool.Config(); cfg.Width != 1920 {
		t.Errorf("Config().Width = %d, want 1920", cfg.Width)
	}

	// Close on uninitialized pool should not panic.
	if err := pool.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
}
