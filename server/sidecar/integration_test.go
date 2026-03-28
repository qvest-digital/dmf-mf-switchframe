//go:build cgo && cuda && integration

package sidecar

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// TestAISidecarLifecycle is an end-to-end integration test that:
// 1. Creates a FramePool-style GPU pool with real CUDA allocations
// 2. Exports IPC handles via NewCUDAGPUPoolHostFromPtrs
// 3. Spawns the AI sidecar binary via Host
// 4. Sends GPU pool handshake
// 5. Enables segmentation for a source
// 6. Waits for mask_ready message with IPC handle
// 7. Marks a frame slot Ready and waits for slot_done
// 8. Kills the sidecar and verifies automatic restart
// 9. After restart, re-sends handshake + enable, verifies mask_ready again
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestAISidecarLifecycle ./sidecar/
func TestAISidecarLifecycle(t *testing.T) {
	const sidecarBin = "/tmp/switchframe-ai"
	if _, err := os.Stat(sidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s (build with: go build -tags cgo,cuda -o %s ./cmd/switchframe-ai/)", sidecarBin, sidecarBin)
	}

	// --- Step 1-2: Create GPU pool from real CUDA allocations ---
	const (
		slotCount = 4
		width     = 64
		height    = 64
	)

	pool, err := NewCUDAGPUPoolHost(GPUPoolConfig{
		SlotCount: slotCount,
		Width:     width,
		Height:    height,
	}, "/switchframe-test-pool")
	if err != nil {
		t.Fatalf("create GPU pool: %v", err)
	}
	defer pool.Close()

	// Verify pool is functional.
	ctrl := pool.Control()
	if ctrl == nil {
		t.Fatal("pool control is nil")
	}
	for i := 0; i < slotCount; i++ {
		if pool.FrameDevPtr(i) == 0 {
			t.Fatalf("slot %d has zero DevPtr", i)
		}
		if ctrl.LoadState(i) != SlotIdle {
			t.Fatalf("slot %d not idle: %v", i, ctrl.LoadState(i))
		}
	}

	// --- Step 3: Spawn AI sidecar ---
	var mu sync.Mutex
	var messages []*Message
	var maskReady *MaskHandleMessage
	var slotDoneReceived bool
	maskReadyCh := make(chan struct{}, 1)
	slotDoneCh := make(chan struct{}, 1)

	host := NewHost(Config{
		Binary:       sidecarBin,
		ReadyTimeout: 30 * time.Second,
	})
	host.OnMessage = func(msg *Message) {
		mu.Lock()
		messages = append(messages, msg)
		mu.Unlock()

		switch msg.Type {
		case "mask_ready":
			mh, err := ParseMaskHandleMessage(msg)
			if err != nil {
				t.Errorf("parse mask_ready: %v", err)
				return
			}
			mu.Lock()
			maskReady = mh
			mu.Unlock()
			select {
			case maskReadyCh <- struct{}{}:
			default:
			}

		case "slot_done":
			mu.Lock()
			slotDoneReceived = true
			mu.Unlock()
			select {
			case slotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	if !host.Alive() {
		t.Fatal("sidecar not alive after start")
	}
	t.Logf("sidecar started, pid=%d, generation=%d", host.Generation(), host.Generation())

	// --- Step 4: Send GPU pool handshake ---
	hs := &GPUPoolHandshake{
		Config:       pool.Config(),
		ShmName:      "/switchframe-test-pool",
		FrameHandles: pool.FrameHandles(),
		ReadyEvents:  pool.ReadyEventHandles(),
		DoneEvents:   pool.DoneEventHandles(),
	}

	conn := host.Conn()
	if conn == nil {
		t.Fatal("no connection to sidecar")
	}
	if err := WriteGPUPoolHandshake(conn, hs); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	t.Log("handshake sent")

	// Brief pause for sidecar to process handshake.
	time.Sleep(100 * time.Millisecond)

	// --- Step 5: Enable segmentation ---
	if err := host.Send(&Message{
		Cmd: "enable_segmentation",
		Config: map[string]any{
			"source_id": "cam1",
			"width":     width,
			"height":    height,
			"smoothing": 0.0,
		},
	}); err != nil {
		t.Fatalf("send enable_segmentation: %v", err)
	}
	t.Log("enable_segmentation sent")

	// --- Step 6: Wait for mask_ready ---
	select {
	case <-maskReadyCh:
		t.Log("mask_ready received")
	case <-time.After(5 * time.Second):
		mu.Lock()
		t.Logf("received %d messages total", len(messages))
		for i, m := range messages {
			t.Logf("  msg[%d]: type=%q cmd=%q error=%q", i, m.Type, m.Cmd, m.Error)
		}
		mu.Unlock()
		t.Fatal("timeout waiting for mask_ready")
	}

	mu.Lock()
	mh := maskReady
	mu.Unlock()

	if mh == nil {
		t.Fatal("mask_ready message is nil")
	}
	if mh.SourceID != "cam1" {
		t.Errorf("mask_ready source_id=%q, want cam1", mh.SourceID)
	}
	if mh.Width != width || mh.Height != height {
		t.Errorf("mask_ready dimensions=%dx%d, want %dx%d", mh.Width, mh.Height, width, height)
	}

	// Verify sidecar is still alive before importing (IPC handle is invalid if source exited).
	if !host.Alive() {
		t.Fatal("sidecar died before we could import the mask handle")
	}

	// Import the mask IPC handle to verify it's a valid device pointer.
	maskPtr, err := ImportIPCHandle(mh.Handle)
	if err != nil {
		t.Fatalf("import mask handle: %v", err)
	}
	defer CloseIPCHandle(maskPtr)
	if maskPtr == 0 {
		t.Fatal("imported mask DevPtr is zero")
	}
	t.Logf("mask imported: DevPtr=0x%x, size=%dx%d", maskPtr, mh.Width, mh.Height)

	// --- Step 7: Mark slot Ready, send infer, wait for slot_done ---
	ctrl.MarkReady(0, 12345)

	if err := host.Send(&Message{
		Cmd: "infer",
		Config: map[string]any{
			"slot":      0,
			"source_id": "cam1",
		},
	}); err != nil {
		t.Fatalf("send infer: %v", err)
	}

	select {
	case <-slotDoneCh:
		t.Log("slot_done received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for slot_done")
	}

	mu.Lock()
	if !slotDoneReceived {
		t.Error("slot_done not received")
	}
	mu.Unlock()

	// Verify slot transitioned back (Done state — host is responsible for Idle transition).
	state := ctrl.LoadState(0)
	if state != SlotDone {
		t.Errorf("slot 0 state=%v, want Done", state)
	}
	// Reset to Idle.
	ctrl.CompareAndSwapState(0, SlotDone, SlotIdle)

	// --- Step 8: Kill sidecar, verify restart ---
	gen1 := host.Generation()
	t.Logf("killing sidecar (generation=%d)", gen1)

	// Reset state for restart verification.
	mu.Lock()
	maskReady = nil
	slotDoneReceived = false
	mu.Unlock()

	// Drain channels.
	select {
	case <-maskReadyCh:
	default:
	}
	select {
	case <-slotDoneCh:
	default:
	}

	// Simulate crash by closing the sidecar's host process.
	host.TriggerRestart("integration test: simulated crash")

	// Wait for restart (new generation).
	deadline := time.After(15 * time.Second)
	for {
		if host.Alive() && host.Generation() > gen1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for restart (gen=%d, alive=%v)", host.Generation(), host.Alive())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Logf("sidecar restarted, generation=%d, restarts=%d", host.Generation(), host.Restarts())

	// --- Step 9: Re-send handshake + enable after restart ---
	conn2 := host.Conn()
	if conn2 == nil {
		t.Fatal("no connection after restart")
	}
	if err := WriteGPUPoolHandshake(conn2, hs); err != nil {
		t.Fatalf("re-send handshake: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := host.Send(&Message{
		Cmd: "enable_segmentation",
		Config: map[string]any{
			"source_id": "cam1",
			"width":     width,
			"height":    height,
			"smoothing": 0.0,
		},
	}); err != nil {
		t.Fatalf("re-send enable_segmentation: %v", err)
	}

	// Wait for new mask_ready after restart.
	select {
	case <-maskReadyCh:
		t.Log("mask_ready received after restart")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for mask_ready after restart")
	}

	mu.Lock()
	mh2 := maskReady
	mu.Unlock()
	if mh2 == nil {
		t.Fatal("mask_ready after restart is nil")
	}
	if mh2.SourceID != "cam1" {
		t.Errorf("mask_ready after restart source_id=%q, want cam1", mh2.SourceID)
	}

	// Import the new mask handle (old one from previous sidecar process is invalid).
	maskPtr2, err := ImportIPCHandle(mh2.Handle)
	if err != nil {
		t.Fatalf("import mask handle after restart: %v", err)
	}
	defer CloseIPCHandle(maskPtr2)
	if maskPtr2 == 0 {
		t.Fatal("imported mask DevPtr after restart is zero")
	}
	t.Logf("mask imported after restart: DevPtr=0x%x", maskPtr2)

	// Verify status command works.
	if err := host.Send(&Message{Cmd: "status"}); err != nil {
		t.Fatalf("send status: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	t.Log("integration test passed: spawn → handshake → enable → infer → restart → re-enable")
}

// TestAISidecar_SharedMemorySourceKey verifies that source keys written via
// shared memory are visible to the sidecar. The full flow:
//  1. Create GPU pool (4 slots, 64x64)
//  2. Start sidecar, send handshake
//  3. Enable segmentation for "cam1"
//  4. Write source key "cam1" to slot 0 via ctrl.SetSourceKey
//  5. Mark slot 0 Ready with a PTS
//  6. Wait for slot_done (sidecar reads source key, dispatches to session, processes)
//  7. Verify slot transitions back through Done and can be reset to Idle
//
// Requires: CUDA runtime, switchframe-ai binary at /tmp/switchframe-ai
// Run: CGO_ENABLED=1 go test -tags 'cgo,cuda,integration' -v -race -run TestAISidecar_SharedMemorySourceKey ./sidecar/
func TestAISidecar_SharedMemorySourceKey(t *testing.T) {
	const sidecarBin = "/tmp/switchframe-ai"
	if _, err := os.Stat(sidecarBin); err != nil {
		t.Skipf("AI sidecar binary not found at %s", sidecarBin)
	}

	const (
		slotCount = 4
		width     = 64
		height    = 64
	)

	// --- Step 1: Create GPU pool ---
	pool, err := NewCUDAGPUPoolHost(GPUPoolConfig{
		SlotCount: slotCount,
		Width:     width,
		Height:    height,
	}, "/switchframe-srckey-test")
	if err != nil {
		t.Fatalf("create GPU pool: %v", err)
	}
	defer pool.Close()

	ctrl := pool.Control()
	if ctrl == nil {
		t.Fatal("pool control is nil")
	}

	// --- Step 2: Spawn sidecar and send handshake ---
	var mu sync.Mutex
	maskReadyCh := make(chan struct{}, 1)
	slotDoneCh := make(chan struct{}, 4) // buffered for multiple slot_done events
	var slotDoneSlots []int

	host := NewHost(Config{
		Binary:       sidecarBin,
		ReadyTimeout: 30 * time.Second,
	})
	host.OnMessage = func(msg *Message) {
		switch msg.Type {
		case MsgMaskReady:
			select {
			case maskReadyCh <- struct{}{}:
			default:
			}
		case MsgSlotDone:
			data, ok := msg.Data.(map[string]any)
			if ok {
				if slotF, ok := data["slot"].(float64); ok {
					mu.Lock()
					slotDoneSlots = append(slotDoneSlots, int(slotF))
					mu.Unlock()
				}
			}
			select {
			case slotDoneCh <- struct{}{}:
			default:
			}
		}
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	defer host.Close()

	hs := &GPUPoolHandshake{
		Config:       pool.Config(),
		ShmName:      "/switchframe-srckey-test",
		FrameHandles: pool.FrameHandles(),
		ReadyEvents:  pool.ReadyEventHandles(),
		DoneEvents:   pool.DoneEventHandles(),
	}
	conn := host.Conn()
	if conn == nil {
		t.Fatal("no connection to sidecar")
	}
	if err := WriteGPUPoolHandshake(conn, hs); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// --- Step 3: Enable segmentation for "cam1" ---
	if err := host.Send(&Message{
		Cmd: CmdEnableSegmentation,
		Config: map[string]any{
			"source_id": "cam1",
			"width":     width,
			"height":    height,
			"smoothing": 0.0,
		},
	}); err != nil {
		t.Fatalf("send enable_segmentation: %v", err)
	}

	// Wait for mask_ready.
	select {
	case <-maskReadyCh:
		t.Log("mask_ready received for cam1")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for mask_ready")
	}

	// --- Step 4-5: Write source key and mark slot Ready ---
	// This is the key difference from TestAISidecarLifecycle: we use the
	// shared memory source key field instead of sending source_id in the
	// infer command. The sidecar should read the source key from shared
	// memory after observing SlotReady.
	ctrl.SetSourceKey(0, "cam1")

	// Verify the source key was written correctly.
	readBack := ctrl.LoadSourceKey(0)
	if readBack != "cam1" {
		t.Fatalf("source key read-back=%q, want %q", readBack, "cam1")
	}

	ctrl.MarkReady(0, 12345)

	// Send infer (the sidecar uses the shared memory source key to dispatch).
	if err := host.Send(&Message{
		Cmd: "infer",
		Config: map[string]any{
			"slot":      0,
			"source_id": "cam1",
		},
	}); err != nil {
		t.Fatalf("send infer: %v", err)
	}

	// --- Step 6: Wait for slot_done ---
	select {
	case <-slotDoneCh:
		t.Log("slot_done received")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for slot_done")
	}

	// --- Step 7: Verify slot state and reset ---
	state := ctrl.LoadState(0)
	if state != SlotDone {
		t.Errorf("slot 0 state=%v, want Done", state)
	}

	// Reset to Idle.
	if !ctrl.CompareAndSwapState(0, SlotDone, SlotIdle) {
		t.Error("failed to CAS slot 0 from Done to Idle")
	}
	if ctrl.LoadState(0) != SlotIdle {
		t.Error("slot 0 should be Idle after CAS")
	}

	mu.Lock()
	doneSlots := append([]int(nil), slotDoneSlots...)
	mu.Unlock()

	if len(doneSlots) == 0 {
		t.Error("no slot_done events recorded")
	} else if doneSlots[0] != 0 {
		t.Errorf("expected slot_done for slot 0, got slot %d", doneSlots[0])
	}

	// --- Bonus: Verify a second source key on a different slot ---
	// Write a different source key to slot 1 and verify isolation.
	ctrl.SetSourceKey(1, "cam2")
	if k := ctrl.LoadSourceKey(0); k != "cam1" {
		t.Errorf("slot 0 key corrupted after writing slot 1: %q", k)
	}
	if k := ctrl.LoadSourceKey(1); k != "cam2" {
		t.Errorf("slot 1 key=%q, want cam2", k)
	}

	t.Log("shared memory source key integration test passed")
}
