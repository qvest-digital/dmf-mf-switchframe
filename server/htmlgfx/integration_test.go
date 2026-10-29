package htmlgfx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForStatus polls until browser browserID has the expected status.
func waitForStatus(t *testing.T, mgr *Manager, browserID int, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		states := mgr.BrowserStates()
		if states[browserID].Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	states := mgr.BrowserStates()
	t.Fatalf("browser %d: status=%q after %v, want %q", browserID, states[browserID].Status, timeout, want)
}

// waitForOverlays polls until the compositor has received at least n overlays.
func waitForOverlays(t *testing.T, comp *mockCompositor, n int, timeout time.Duration) []overlayCall {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		overlays := comp.getOverlays()
		if len(overlays) >= n {
			return overlays
		}
		time.Sleep(5 * time.Millisecond)
	}
	overlays := comp.getOverlays()
	t.Fatalf("expected at least %d overlays, got %d after %v", n, len(overlays), timeout)
	return nil
}

// makeBGRAFrame creates a uniform BGRA frame (all pixels the same colour).
func makeBGRAFrame(browserID uint8, width, height uint16, b, g, r, a byte) Frame {
	pixels := make([]byte, int(width)*int(height)*4)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i] = b
		pixels[i+1] = g
		pixels[i+2] = r
		pixels[i+3] = a
	}
	return Frame{
		BrowserID: browserID,
		Width:     width,
		Height:    height,
		Pixels:    pixels,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_FullLifecycle(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	const layerID = 3

	// 1. All browsers start empty.
	for _, bs := range mgr.BrowserStates() {
		if bs.Status != "empty" {
			t.Fatalf("initial status: got %q, want %q", bs.Status, "empty")
		}
	}

	// 2. Allocate browser on layer 3.
	id, err := mgr.AllocateBrowser(layerID)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}
	if s := mgr.BrowserStates()[id].Status; s != "allocated" {
		t.Fatalf("after allocate: got %q, want %q", s, "allocated")
	}
	mock.waitForCommands(1, 2*time.Second) // create

	// 3. Load URL -> status becomes "loading".
	if err := mgr.LoadURL(id, "https://templates.test/lower-third.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}
	if s := mgr.BrowserStates()[id].Status; s != "loading" {
		t.Fatalf("after LoadURL: got %q, want %q", s, "loading")
	}
	mock.waitForCommands(2, 2*time.Second) // create + load

	// 4. Sidecar sends format_detected -> status becomes "ready".
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "unknown",
	}); err != nil {
		t.Fatalf("sendResponse format_detected: %v", err)
	}
	waitForStatus(t, mgr, id, "ready", 2*time.Second)
	if f := mgr.BrowserStates()[id].Format; f != "unknown" {
		t.Fatalf("format: got %q, want %q", f, "unknown")
	}

	// 5. Update data.
	if err := mgr.Update(id, `{"headline":"Breaking News"}`); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mock.waitForCommands(3, 2*time.Second) // create + load + update

	// 6. Play -> status becomes "playing".
	if err := mgr.Play(id); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if s := mgr.BrowserStates()[id].Status; s != "playing" {
		t.Fatalf("after Play: got %q, want %q", s, "playing")
	}
	mock.waitForCommands(4, 2*time.Second) // create + load + update + play

	// 7. Receive a frame -> compositor gets RGBA overlay.
	frame := makeBGRAFrame(uint8(id), 4, 4, 0, 0, 255, 255) // opaque red in BGRA
	if err := mock.sendFrame(frame); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}
	overlays := waitForOverlays(t, comp, 1, 2*time.Second)
	if overlays[0].id != layerID {
		t.Errorf("overlay layer: got %d, want %d", overlays[0].id, layerID)
	}

	// 8. Stop -> status becomes "stopped".
	if err := mgr.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s := mgr.BrowserStates()[id].Status; s != "stopped" {
		t.Fatalf("after Stop: got %q, want %q", s, "stopped")
	}

	// 9. Free -> status becomes "empty".
	mgr.FreeBrowser(id)
	if s := mgr.BrowserStates()[id].Status; s != "empty" {
		t.Fatalf("after Free: got %q, want %q", s, "empty")
	}

	// 10. Verify full command sequence.
	cmds := mock.waitForCommands(6, 2*time.Second) // create, load, update, play, stop, destroy
	if len(cmds) < 6 {
		t.Fatalf("expected at least 6 commands, got %d", len(cmds))
	}
	expectedCmds := []string{"create", "load", "update", "play", "stop", "destroy"}
	for i, want := range expectedCmds {
		if cmds[i].Cmd != want {
			t.Errorf("command %d: got %q, want %q", i, cmds[i].Cmd, want)
		}
	}
}

func TestIntegration_MultipleTemplates(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	const layer5 = 5
	const layer7 = 7

	// Allocate two browsers on different layers.
	id0, err := mgr.AllocateBrowser(layer5)
	if err != nil {
		t.Fatalf("AllocateBrowser(layer5): %v", err)
	}
	id1, err := mgr.AllocateBrowser(layer7)
	if err != nil {
		t.Fatalf("AllocateBrowser(layer7): %v", err)
	}
	mock.waitForCommands(2, 2*time.Second) // two creates

	// Load different URLs on each.
	if err := mgr.LoadURL(id0, "https://templates.test/lower-third.html"); err != nil {
		t.Fatalf("LoadURL(0): %v", err)
	}
	if err := mgr.LoadURL(id1, "https://templates.test/scorebug.html"); err != nil {
		t.Fatalf("LoadURL(1): %v", err)
	}
	mock.waitForCommands(4, 2*time.Second) // 2 creates + 2 loads

	// Send frames for browser 0 (blue in BGRA = RGBA blue).
	frame0 := makeBGRAFrame(uint8(id0), 2, 2, 255, 0, 0, 255) // B=255 in BGRA
	if err := mock.sendFrame(frame0); err != nil {
		t.Fatalf("sendFrame(0): %v", err)
	}

	// Send frames for browser 1 (green).
	frame1 := makeBGRAFrame(uint8(id1), 2, 2, 0, 255, 0, 255) // G=255
	if err := mock.sendFrame(frame1); err != nil {
		t.Fatalf("sendFrame(1): %v", err)
	}

	// Wait for both overlays to arrive.
	overlays := waitForOverlays(t, comp, 2, 2*time.Second)

	// Verify correct layer mapping.
	layersSeen := map[int]bool{}
	for _, ov := range overlays {
		layersSeen[ov.id] = true
	}
	if !layersSeen[layer5] {
		t.Errorf("no overlay delivered to layer %d", layer5)
	}
	if !layersSeen[layer7] {
		t.Errorf("no overlay delivered to layer %d", layer7)
	}

	// Free browser 0 and verify browser 1 continues working.
	mgr.FreeBrowser(id0)

	// BrowserForLayer should still find browser 1 on layer 7.
	if bid := mgr.BrowserForLayer(layer7); bid != id1 {
		t.Errorf("BrowserForLayer(%d) after freeing 0: got %d, want %d", layer7, bid, id1)
	}
	// Layer 5 should be unmapped.
	if bid := mgr.BrowserForLayer(layer5); bid != -1 {
		t.Errorf("BrowserForLayer(%d) after freeing 0: got %d, want -1", layer5, bid)
	}

	// Send another frame for browser 1, verify it still arrives.
	frame1b := makeBGRAFrame(uint8(id1), 2, 2, 0, 128, 0, 255)
	if err := mock.sendFrame(frame1b); err != nil {
		t.Fatalf("sendFrame(1b): %v", err)
	}
	overlays = waitForOverlays(t, comp, 3, 2*time.Second)
	last := overlays[len(overlays)-1]
	if last.id != layer7 {
		t.Errorf("last overlay layer: got %d, want %d", last.id, layer7)
	}
}

func TestIntegration_CasparCGFormat(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Load a CasparCG template URL.
	if err := mgr.LoadURL(id, "https://templates.test/casparcg-template.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}

	// Sidecar detects CasparCG format.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "casparcg",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	waitForStatus(t, mgr, id, "ready", 2*time.Second)
	bs := mgr.BrowserStates()[id]
	if bs.Format != "casparcg" {
		t.Errorf("format: got %q, want %q", bs.Format, "casparcg")
	}

	// Send CasparCG-style commands: update, play, next, stop.
	if err := mgr.Update(id, `{"f0":"Hello","f1":"World"}`); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mgr.Play(id); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := mgr.Next(id); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := mgr.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify all commands were forwarded in order.
	// Expected: create, load, update, play, next, stop
	cmds := mock.waitForCommands(6, 2*time.Second)
	if len(cmds) < 6 {
		t.Fatalf("expected at least 6 commands, got %d", len(cmds))
	}
	expected := []string{"create", "load", "update", "play", "next", "stop"}
	for i, want := range expected {
		if cmds[i].Cmd != want {
			t.Errorf("command %d: got %q, want %q", i, cmds[i].Cmd, want)
		}
		// All commands after create should carry the browser ID.
		if i > 0 && (cmds[i].ID == nil || *cmds[i].ID != id) {
			t.Errorf("command %d (%s): id=%v, want %d", i, want, cmds[i].ID, id)
		}
	}
}

func TestIntegration_OGrafFormat(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(2)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	if err := mgr.LoadURL(id, "https://templates.test/ograf-template.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}

	// Sidecar detects OGraf format.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "ograf",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	waitForStatus(t, mgr, id, "ready", 2*time.Second)
	bs := mgr.BrowserStates()[id]
	if bs.Format != "ograf" {
		t.Errorf("format: got %q, want %q", bs.Format, "ograf")
	}

	// Send a custom action (OGraf-specific).
	if err := mgr.CustomAction(id, "animate_in", `{"speed":1.5}`); err != nil {
		t.Fatalf("CustomAction: %v", err)
	}

	// Verify custom command was forwarded.
	cmds := mock.waitForCommands(3, 2*time.Second) // create, load, custom
	if len(cmds) < 3 {
		t.Fatalf("expected at least 3 commands, got %d", len(cmds))
	}
	last := cmds[2]
	if last.Cmd != "custom" {
		t.Errorf("last command: got %q, want %q", last.Cmd, "custom")
	}
	if last.Action != "animate_in" {
		t.Errorf("action: got %q, want %q", last.Action, "animate_in")
	}
	if last.Payload != `{"speed":1.5}` {
		t.Errorf("payload: got %q, want %q", last.Payload, `{"speed":1.5}`)
	}
}

func TestIntegration_LoadError(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	if err := mgr.LoadURL(id, "https://templates.test/nonexistent.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}

	// Sidecar sends load_error.
	if err := mock.sendResponse(ControlResponse{
		ID:    id,
		Event: "load_error",
		Error: "ERR_FILE_NOT_FOUND",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	waitForStatus(t, mgr, id, "error", 2*time.Second)
}

func TestIntegration_BGRAToRGBAVerification(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	const layerID = 1

	id, err := mgr.AllocateBrowser(layerID)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Create a 4x4 opaque blue frame in BGRA: B=255, G=0, R=0, A=255.
	frame := makeBGRAFrame(uint8(id), 4, 4, 255, 0, 0, 255)
	if err := mock.sendFrame(frame); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	overlays := waitForOverlays(t, comp, 1, 2*time.Second)
	ov := overlays[0]

	if ov.id != layerID {
		t.Errorf("overlay layer: got %d, want %d", ov.id, layerID)
	}
	if ov.width != 4 || ov.height != 4 {
		t.Errorf("overlay dimensions: got %dx%d, want 4x4", ov.width, ov.height)
	}

	// Verify every pixel is correct RGBA (R=0, G=0, B=255, A=255).
	for i := 0; i < len(ov.rgba); i += 4 {
		r, g, b, a := ov.rgba[i], ov.rgba[i+1], ov.rgba[i+2], ov.rgba[i+3]
		if r != 0 || g != 0 || b != 255 || a != 255 {
			t.Errorf("pixel %d: got RGBA(%d,%d,%d,%d), want (0,0,255,255)", i/4, r, g, b, a)
			break
		}
	}

	// Also test semi-transparent magenta: BGRA(255, 0, 128, 128).
	// Premultiplied: actual B=255 but premul with A=128 means channel was scaled.
	// The bgraToRGBA un-premultiply should recover the straight alpha values.
	semiFrame := makeBGRAFrame(uint8(id), 2, 2, 128, 0, 64, 128)
	// Premultiplied BGRA: B=128, G=0, R=64, A=128
	// Un-premultiply: R = min((64*255 + 64)/128, 255) = min(128, 255) = 128
	//                 G = min((0*255 + 64)/128, 255) = 0
	//                 B = min((128*255 + 64)/128, 255) = min(255, 255) = 255
	if err := mock.sendFrame(semiFrame); err != nil {
		t.Fatalf("sendFrame semi: %v", err)
	}

	overlays = waitForOverlays(t, comp, 2, 2*time.Second)
	ov2 := overlays[1]
	r, g, b, a := ov2.rgba[0], ov2.rgba[1], ov2.rgba[2], ov2.rgba[3]
	if a != 128 {
		t.Errorf("semi-transparent pixel alpha: got %d, want 128", a)
	}
	// R should be un-premultiplied from 64: (64*255+64)/128 = 128
	if r != 128 {
		t.Errorf("semi-transparent pixel R: got %d, want 128", r)
	}
	if g != 0 {
		t.Errorf("semi-transparent pixel G: got %d, want 0", g)
	}
	// B should be un-premultiplied from 128: (128*255+64)/128 = 255
	if b != 255 {
		t.Errorf("semi-transparent pixel B: got %d, want 255", b)
	}
}

func TestIntegration_FrameDeliveryTiming(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Send 10 frames rapidly with different pixel values so we can
	// distinguish them.
	const numFrames = 10
	for i := 0; i < numFrames; i++ {
		frame := makeBGRAFrame(uint8(id), 2, 2, byte(i), 0, 0, 255)
		if err := mock.sendFrame(frame); err != nil {
			t.Fatalf("sendFrame(%d): %v", i, err)
		}
	}

	// The compositor should eventually receive all 10 frames. The
	// frameReadLoop reads sequentially so nothing should be dropped.
	overlays := waitForOverlays(t, comp, numFrames, 3*time.Second)

	if len(overlays) < numFrames {
		t.Fatalf("expected at least %d overlays, got %d", numFrames, len(overlays))
	}

	// Verify the last overlay has the expected pixel data from frame 9.
	// BGRA(9,0,0,255) -> RGBA(0,0,9,255) after conversion.
	last := overlays[numFrames-1]
	if last.rgba[2] != 9 { // B channel in RGBA = original B from BGRA
		t.Errorf("last frame B channel: got %d, want 9", last.rgba[2])
	}
}

func TestIntegration_StateCallbacks(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	var count atomic.Int32

	mgr.OnStateChange(func() {
		count.Add(1)
	})

	// 1. AllocateBrowser fires state change.
	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}
	mock.waitForCommands(1, 2*time.Second)

	// 2. LoadURL fires state change.
	if err := mgr.LoadURL(id, "https://templates.test/test.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}
	mock.waitForCommands(2, 2*time.Second)

	// 3. format_detected from sidecar fires state change.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "unknown",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}
	waitForStatus(t, mgr, id, "ready", 2*time.Second)

	// 4. Play fires state change.
	if err := mgr.Play(id); err != nil {
		t.Fatalf("Play: %v", err)
	}
	mock.waitForCommands(3, 2*time.Second)

	// 5. Stop fires state change.
	if err := mgr.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	mock.waitForCommands(4, 2*time.Second)

	// Wait a bit for all async callbacks to settle.
	time.Sleep(50 * time.Millisecond)

	// Expected state changes:
	// 1. AllocateBrowser (allocated)
	// 2. LoadURL (loading)
	// 3. format_detected (ready) - from controlReadLoop
	// 4. Play (playing)
	// 5. Stop (stopped)
	got := int(count.Load())
	if got < 5 {
		t.Errorf("state change callback count: got %d, want >= 5", got)
	}
}

func TestIntegration_PoolExhaustion_Recovery(t *testing.T) {
	comp := &mockCompositor{}
	cfg := Config{
		PoolSize: 4,
		Width:    1920,
		Height:   1080,
		FPS:      30,
	}
	mgr := NewManager(comp, cfg)

	mock := newMockSidecar(t)
	defer mock.close()

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- mgr.connectSockets(mock.controlPath, mock.framePath)
	}()
	mock.acceptConnections(t)

	if err := <-connectDone; err != nil {
		t.Fatalf("connectSockets: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	go mock.readCommands()

	// Allocate all 4 slots.
	ids := make([]int, cfg.PoolSize)
	for i := 0; i < cfg.PoolSize; i++ {
		id, err := mgr.AllocateBrowser(i)
		if err != nil {
			t.Fatalf("AllocateBrowser(%d): %v", i, err)
		}
		ids[i] = id
	}

	// 5th allocation should fail.
	_, err := mgr.AllocateBrowser(99)
	if err == nil {
		t.Fatal("expected error when pool is full, got nil")
	}

	// Free slot 2.
	mgr.FreeBrowser(ids[2])

	// Now allocating should succeed and reuse slot 2.
	newID, err := mgr.AllocateBrowser(42)
	if err != nil {
		t.Fatalf("AllocateBrowser after free: %v", err)
	}
	if newID != ids[2] {
		t.Errorf("reused slot: got %d, want %d", newID, ids[2])
	}

	// Verify the new allocation has the correct layer.
	states := mgr.BrowserStates()
	if states[newID].LayerID != 42 {
		t.Errorf("reused slot layerID: got %d, want 42", states[newID].LayerID)
	}
	if states[newID].Status != "allocated" {
		t.Errorf("reused slot status: got %q, want %q", states[newID].Status, "allocated")
	}
}

func TestIntegration_RapidAllocFree(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	const iterations = 20

	for i := 0; i < iterations; i++ {
		id, err := mgr.AllocateBrowser(i)
		if err != nil {
			t.Fatalf("iteration %d: AllocateBrowser: %v", i, err)
		}
		mgr.FreeBrowser(id)
	}

	// All slots should be empty after rapid alloc/free.
	for _, bs := range mgr.BrowserStates() {
		if bs.Status != "empty" {
			t.Errorf("browser %d status: got %q, want %q", bs.ID, bs.Status, "empty")
		}
		if bs.LayerID != -1 {
			t.Errorf("browser %d layerID: got %d, want -1", bs.ID, bs.LayerID)
		}
	}

	// All alloc/free cycles used the same slot (slot 0) each time,
	// so we expect iterations * 2 commands (create + destroy each).
	cmds := mock.waitForCommands(iterations*2, 3*time.Second)
	if len(cmds) < iterations*2 {
		t.Fatalf("expected at least %d commands, got %d", iterations*2, len(cmds))
	}

	// Verify alternating create/destroy pattern.
	for i := 0; i < iterations*2; i += 2 {
		if cmds[i].Cmd != "create" {
			t.Errorf("command %d: got %q, want %q", i, cmds[i].Cmd, "create")
		}
		if cmds[i+1].Cmd != "destroy" {
			t.Errorf("command %d: got %q, want %q", i+1, cmds[i+1].Cmd, "destroy")
		}
	}
}

func TestIntegration_ConcurrentAllocFree(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Stress test: 8 goroutines each doing 10 alloc/free cycles.
	const goroutines = 8
	const cyclesPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for c := 0; c < cyclesPerGoroutine; c++ {
				id, err := mgr.AllocateBrowser(0)
				if err != nil {
					// Pool exhaustion is expected under contention.
					continue
				}
				mgr.FreeBrowser(id)
			}
		}()
	}

	wg.Wait()

	// After all goroutines finish, all slots should be empty.
	for _, bs := range mgr.BrowserStates() {
		if bs.Status != "empty" {
			t.Errorf("browser %d status after concurrent stress: got %q, want %q",
				bs.ID, bs.Status, "empty")
		}
	}
}

func TestIntegration_FrameForUnmappedBrowser(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Allocate browser 0, then free it.
	id, err := mgr.AllocateBrowser(1)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}
	mgr.FreeBrowser(id)

	// Send a frame for the now-freed browser. It should be silently
	// dropped (layerID = -1).
	frame := makeBGRAFrame(uint8(id), 2, 2, 255, 0, 0, 255)
	if err := mock.sendFrame(frame); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	// Also send a frame for a never-allocated browser.
	frame2 := makeBGRAFrame(3, 2, 2, 0, 255, 0, 255)
	if err := mock.sendFrame(frame2); err != nil {
		t.Fatalf("sendFrame(3): %v", err)
	}

	// Give the frame read loop time to process.
	time.Sleep(100 * time.Millisecond)

	overlays := comp.getOverlays()
	if len(overlays) != 0 {
		t.Errorf("expected 0 overlays for unmapped browsers, got %d", len(overlays))
	}
}

func TestIntegration_ShutdownDuringFrameDelivery(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Start sending frames.
	frame := makeBGRAFrame(uint8(id), 2, 2, 100, 100, 100, 255)
	if err := mock.sendFrame(frame); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	// Wait for at least one overlay.
	waitForOverlays(t, comp, 1, 2*time.Second)

	// Close the manager while frames might still be in flight.
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// No panic = success. The frame read loop should exit cleanly.
}

func TestIntegration_MultipleFormatDetections(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Load first URL.
	if err := mgr.LoadURL(id, "https://templates.test/first.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}

	// First format detection: ograf.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "ograf",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}
	waitForStatus(t, mgr, id, "ready", 2*time.Second)
	if f := mgr.BrowserStates()[id].Format; f != "ograf" {
		t.Errorf("first format: got %q, want %q", f, "ograf")
	}

	// Load a different URL (simulating template switch).
	if err := mgr.LoadURL(id, "https://templates.test/second.html"); err != nil {
		t.Fatalf("LoadURL(2): %v", err)
	}

	// Second format detection: casparcg.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "casparcg",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}
	waitForStatus(t, mgr, id, "ready", 2*time.Second)
	if f := mgr.BrowserStates()[id].Format; f != "casparcg" {
		t.Errorf("second format: got %q, want %q", f, "casparcg")
	}
}
