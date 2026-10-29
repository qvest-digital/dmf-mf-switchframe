package htmlgfx

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCompositor records SetOverlay calls.
type mockCompositor struct {
	mu       sync.Mutex
	overlays []overlayCall
}

type overlayCall struct {
	id       int
	rgba     []byte
	width    int
	height   int
	template string
}

func (mc *mockCompositor) SetOverlay(id int, rgba []byte, width, height int, template string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	cp := make([]byte, len(rgba))
	copy(cp, rgba)
	mc.overlays = append(mc.overlays, overlayCall{id, cp, width, height, template})
	return nil
}

func (mc *mockCompositor) getOverlays() []overlayCall {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	cp := make([]overlayCall, len(mc.overlays))
	copy(cp, mc.overlays)
	return cp
}

// mockSidecar listens on Unix sockets and simulates the CEF sidecar.
type mockSidecar struct {
	controlPath string
	framePath   string

	controlListener net.Listener
	frameListener   net.Listener

	// accepted connections
	mu          sync.Mutex
	controlConn net.Conn
	frameConn   net.Conn

	// commands received on the control socket
	cmdMu    sync.Mutex
	commands []ControlCommand

	// for sending responses and frames back
	done chan struct{}
}

func newMockSidecar(t *testing.T) *mockSidecar {
	t.Helper()
	// Use a short temp dir to avoid hitting macOS 104-byte Unix socket path limit.
	dir, err := os.MkdirTemp("", "hgfx")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	controlPath := filepath.Join(dir, "c.sock")
	framePath := filepath.Join(dir, "f.sock")

	controlLn, err := net.Listen("unix", controlPath)
	if err != nil {
		t.Fatalf("listen control: %v", err)
	}

	frameLn, err := net.Listen("unix", framePath)
	if err != nil {
		_ = controlLn.Close()
		t.Fatalf("listen frame: %v", err)
	}

	ms := &mockSidecar{
		controlPath:     controlPath,
		framePath:       framePath,
		controlListener: controlLn,
		frameListener:   frameLn,
		done:            make(chan struct{}),
	}

	return ms
}

// acceptConnections accepts one connection each for control and frame sockets.
func (ms *mockSidecar) acceptConnections(t *testing.T) {
	t.Helper()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		conn, err := ms.controlListener.Accept()
		if err != nil {
			return
		}
		ms.mu.Lock()
		ms.controlConn = conn
		ms.mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		conn, err := ms.frameListener.Accept()
		if err != nil {
			return
		}
		ms.mu.Lock()
		ms.frameConn = conn
		ms.mu.Unlock()
	}()

	wg.Wait()
}

// readCommands reads control commands until the connection closes. Must be
// called after acceptConnections.
func (ms *mockSidecar) readCommands() {
	ms.mu.Lock()
	conn := ms.controlConn
	ms.mu.Unlock()
	if conn == nil {
		return
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var cmd ControlCommand
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			continue
		}
		ms.cmdMu.Lock()
		ms.commands = append(ms.commands, cmd)
		ms.cmdMu.Unlock()
	}
}

func (ms *mockSidecar) getCommands() []ControlCommand {
	ms.cmdMu.Lock()
	defer ms.cmdMu.Unlock()
	cp := make([]ControlCommand, len(ms.commands))
	copy(cp, ms.commands)
	return cp
}

// sendResponse sends a control response back to the manager.
func (ms *mockSidecar) sendResponse(resp ControlResponse) error {
	ms.mu.Lock()
	conn := ms.controlConn
	ms.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("no control connection")
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// sendFrame sends a binary frame on the frame socket.
func (ms *mockSidecar) sendFrame(f Frame) error {
	ms.mu.Lock()
	conn := ms.frameConn
	ms.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("no frame connection")
	}
	return WriteFrame(conn, f)
}

func (ms *mockSidecar) close() {
	_ = ms.controlListener.Close()
	_ = ms.frameListener.Close()

	ms.mu.Lock()
	if ms.controlConn != nil {
		_ = ms.controlConn.Close()
	}
	if ms.frameConn != nil {
		_ = ms.frameConn.Close()
	}
	ms.mu.Unlock()
}

// waitForCommands polls until at least n commands have been received or timeout.
func (ms *mockSidecar) waitForCommands(n int, timeout time.Duration) []ControlCommand {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmds := ms.getCommands()
		if len(cmds) >= n {
			return cmds
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ms.getCommands()
}

// setupManagerWithMock creates a mock sidecar, connects a manager to it,
// and starts the read loops. Returns the manager, mock, and compositor.
// The caller should defer mock.close() and manager.Close().
func setupManagerWithMock(t *testing.T) (*Manager, *mockSidecar, *mockCompositor) {
	t.Helper()

	comp := &mockCompositor{}
	cfg := Config{
		PoolSize: 4,
		Width:    1920,
		Height:   1080,
		FPS:      30,
	}
	mgr := NewManager(comp, cfg)

	mock := newMockSidecar(t)

	// Accept connections and connect the manager concurrently.
	connectDone := make(chan error, 1)
	go func() {
		connectDone <- mgr.connectSockets(mock.controlPath, mock.framePath)
	}()
	mock.acceptConnections(t)

	if err := <-connectDone; err != nil {
		mock.close()
		t.Fatalf("connectSockets: %v", err)
	}

	// Start reading commands in the background.
	go mock.readCommands()

	// Start the restart loop so tests can exercise restart behavior.
	go mgr.restartLoop()

	return mgr, mock, comp
}

func TestManagerConnect(t *testing.T) {
	comp := &mockCompositor{}
	cfg := DefaultConfig()
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

	// Verify the manager has connections by checking that BrowserStates
	// returns the expected pool size.
	states := mgr.BrowserStates()
	if len(states) != cfg.PoolSize {
		t.Errorf("pool size: got %d, want %d", len(states), cfg.PoolSize)
	}

	for i, s := range states {
		if s.ID != i {
			t.Errorf("browser %d ID: got %d", i, s.ID)
		}
		if s.Status != "empty" {
			t.Errorf("browser %d status: got %q, want %q", i, s.Status, "empty")
		}
		if s.LayerID != -1 {
			t.Errorf("browser %d layerID: got %d, want -1", i, s.LayerID)
		}
	}
}

func TestManagerAllocateBrowser(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Allocate first browser for layer 0.
	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser(0): %v", err)
	}
	if id != 0 {
		t.Errorf("first allocate: got id %d, want 0", id)
	}

	// Allocate second browser for layer 1.
	id2, err := mgr.AllocateBrowser(1)
	if err != nil {
		t.Fatalf("AllocateBrowser(1): %v", err)
	}
	if id2 != 1 {
		t.Errorf("second allocate: got id %d, want 1", id2)
	}

	// Check state.
	states := mgr.BrowserStates()
	if states[0].Status != "allocated" {
		t.Errorf("browser 0 status: got %q, want %q", states[0].Status, "allocated")
	}
	if states[0].LayerID != 0 {
		t.Errorf("browser 0 layerID: got %d, want 0", states[0].LayerID)
	}
	if states[1].Status != "allocated" {
		t.Errorf("browser 1 status: got %q, want %q", states[1].Status, "allocated")
	}
	if states[1].LayerID != 1 {
		t.Errorf("browser 1 layerID: got %d, want 1", states[1].LayerID)
	}

	// Verify create commands were sent.
	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	for i := 0; i < 2; i++ {
		if cmds[i].Cmd != "create" {
			t.Errorf("command %d: got cmd %q, want %q", i, cmds[i].Cmd, "create")
		}
		if cmds[i].ID == nil || *cmds[i].ID != i {
			t.Errorf("command %d: got id %v, want %d", i, cmds[i].ID, i)
		}
		if cmds[i].Width != 1920 {
			t.Errorf("command %d: got width %d, want 1920", i, cmds[i].Width)
		}
		if cmds[i].Height != 1080 {
			t.Errorf("command %d: got height %d, want 1080", i, cmds[i].Height)
		}
		if cmds[i].FPS != 30 {
			t.Errorf("command %d: got fps %d, want 30", i, cmds[i].FPS)
		}
	}
}

func TestManagerFreeBrowser(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Allocate browser.
	id, err := mgr.AllocateBrowser(5)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Wait for the create command.
	mock.waitForCommands(1, 2*time.Second)

	// Free it.
	mgr.FreeBrowser(id)

	// Wait for the destroy command.
	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	found := false
	for _, cmd := range cmds {
		if cmd.Cmd == "destroy" && cmd.ID != nil && *cmd.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no destroy command found for browser %d", id)
	}

	// Verify state is reset.
	states := mgr.BrowserStates()
	if states[id].Status != "empty" {
		t.Errorf("browser %d status after free: got %q, want %q", id, states[id].Status, "empty")
	}
	if states[id].LayerID != -1 {
		t.Errorf("browser %d layerID after free: got %d, want -1", id, states[id].LayerID)
	}

	// Verify the slot is reusable by allocating again.
	id2, err := mgr.AllocateBrowser(7)
	if err != nil {
		t.Fatalf("AllocateBrowser after free: %v", err)
	}
	if id2 != id {
		t.Errorf("reused slot: got id %d, want %d", id2, id)
	}
}

func TestManagerFrameDelivery(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Allocate browser 0 for layer 3.
	_, err := mgr.AllocateBrowser(3)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Create a small 2x2 BGRA frame.
	width := uint16(2)
	height := uint16(2)
	bgra := make([]byte, int(width)*int(height)*4)
	// Opaque red in BGRA: B=0, G=0, R=255, A=255
	for i := 0; i < len(bgra); i += 4 {
		bgra[i] = 0     // B
		bgra[i+1] = 0   // G
		bgra[i+2] = 255 // R
		bgra[i+3] = 255 // A
	}

	f := Frame{
		BrowserID: 0,
		Width:     width,
		Height:    height,
		Pixels:    bgra,
	}

	if err := mock.sendFrame(f); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	// Wait for the compositor to receive the overlay.
	deadline := time.Now().Add(2 * time.Second)
	var overlays []overlayCall
	for time.Now().Before(deadline) {
		overlays = comp.getOverlays()
		if len(overlays) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if len(overlays) == 0 {
		t.Fatal("no overlay calls received")
	}

	ov := overlays[0]
	if ov.id != 3 {
		t.Errorf("overlay layer ID: got %d, want 3", ov.id)
	}
	if ov.width != 2 {
		t.Errorf("overlay width: got %d, want 2", ov.width)
	}
	if ov.height != 2 {
		t.Errorf("overlay height: got %d, want 2", ov.height)
	}
	if ov.template != "html5" {
		t.Errorf("overlay template: got %q, want %q", ov.template, "html5")
	}

	// Verify BGRA was converted to RGBA.
	// Opaque red BGRA -> RGBA: R=255, G=0, B=0, A=255
	for i := 0; i < len(ov.rgba); i += 4 {
		r, g, b, a := ov.rgba[i], ov.rgba[i+1], ov.rgba[i+2], ov.rgba[i+3]
		if r != 255 || g != 0 || b != 0 || a != 255 {
			t.Errorf("pixel %d: got RGBA(%d,%d,%d,%d), want (255,0,0,255)", i/4, r, g, b, a)
			break
		}
	}
}

func TestManagerLoadAndPlay(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Wait for create command.
	mock.waitForCommands(1, 2*time.Second)

	// Load URL.
	if err := mgr.LoadURL(id, "https://templates.test/test.html"); err != nil {
		t.Fatalf("LoadURL: %v", err)
	}

	// Play.
	if err := mgr.Play(id); err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Wait for commands: create + load + play = 3.
	cmds := mock.waitForCommands(3, 2*time.Second)
	if len(cmds) < 3 {
		t.Fatalf("expected at least 3 commands, got %d", len(cmds))
	}

	if cmds[1].Cmd != "load" {
		t.Errorf("command 1: got %q, want %q", cmds[1].Cmd, "load")
	}
	if cmds[1].URL != "https://templates.test/test.html" {
		t.Errorf("command 1 url: got %q, want %q", cmds[1].URL, "https://templates.test/test.html")
	}
	if cmds[1].ID == nil || *cmds[1].ID != id {
		t.Errorf("command 1 id: got %v, want %d", cmds[1].ID, id)
	}

	if cmds[2].Cmd != "play" {
		t.Errorf("command 2: got %q, want %q", cmds[2].Cmd, "play")
	}
	if cmds[2].ID == nil || *cmds[2].ID != id {
		t.Errorf("command 2 id: got %v, want %d", cmds[2].ID, id)
	}

	// Check state.
	states := mgr.BrowserStates()
	if states[id].URL != "https://templates.test/test.html" {
		t.Errorf("browser URL: got %q, want %q", states[id].URL, "https://templates.test/test.html")
	}
	if states[id].Status != "playing" {
		t.Errorf("browser status: got %q, want %q", states[id].Status, "playing")
	}
}

func TestManagerBrowserForLayer(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// No browsers allocated yet.
	if id := mgr.BrowserForLayer(5); id != -1 {
		t.Errorf("BrowserForLayer(5) before allocate: got %d, want -1", id)
	}

	// Allocate browser 0 for layer 5.
	bid, err := mgr.AllocateBrowser(5)
	if err != nil {
		t.Fatalf("AllocateBrowser(5): %v", err)
	}

	if id := mgr.BrowserForLayer(5); id != bid {
		t.Errorf("BrowserForLayer(5): got %d, want %d", id, bid)
	}

	// Layer 99 is not mapped.
	if id := mgr.BrowserForLayer(99); id != -1 {
		t.Errorf("BrowserForLayer(99): got %d, want -1", id)
	}

	// Free the browser, layer mapping should be gone.
	mgr.FreeBrowser(bid)
	if id := mgr.BrowserForLayer(5); id != -1 {
		t.Errorf("BrowserForLayer(5) after free: got %d, want -1", id)
	}
}

func TestManagerPoolExhaustion(t *testing.T) {
	comp := &mockCompositor{}
	cfg := Config{
		PoolSize: 2, // Small pool.
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

	// Fill all slots.
	for i := 0; i < cfg.PoolSize; i++ {
		_, err := mgr.AllocateBrowser(i)
		if err != nil {
			t.Fatalf("AllocateBrowser(%d): %v", i, err)
		}
	}

	// Next should fail.
	_, err := mgr.AllocateBrowser(99)
	if err == nil {
		t.Fatal("expected error when pool is full, got nil")
	}
}

func TestManagerControlResponseFormatDetected(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Allocate a browser.
	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Send format_detected event from sidecar.
	if err := mock.sendResponse(ControlResponse{
		ID:     id,
		Event:  "format_detected",
		Format: "ograf",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	// Wait for the state to update.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		states := mgr.BrowserStates()
		if states[id].Format == "ograf" && states[id].Status == "ready" {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}

	states := mgr.BrowserStates()
	t.Errorf("browser %d format=%q status=%q, want format=%q status=%q",
		id, states[id].Format, states[id].Status, "ograf", "ready")
}

func TestManagerControlResponseLoadError(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Send load_error event.
	if err := mock.sendResponse(ControlResponse{
		ID:    id,
		Event: "load_error",
		Error: "404 not found",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	// Wait for the state to update.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		states := mgr.BrowserStates()
		if states[id].Status == "error" {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}

	states := mgr.BrowserStates()
	t.Errorf("browser %d status=%q, want %q", id, states[id].Status, "error")
}

func TestManagerControlResponseStopped(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	// Send stopped event.
	if err := mock.sendResponse(ControlResponse{
		ID:    id,
		Event: "stopped",
	}); err != nil {
		t.Fatalf("sendResponse: %v", err)
	}

	// Wait for the state to update.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		states := mgr.BrowserStates()
		if states[id].Status == "stopped" {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}

	states := mgr.BrowserStates()
	t.Errorf("browser %d status=%q, want %q", id, states[id].Status, "stopped")
}

func TestManagerStopCommand(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	mock.waitForCommands(1, 2*time.Second)

	if err := mgr.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	if cmds[1].Cmd != "stop" {
		t.Errorf("command 1: got %q, want %q", cmds[1].Cmd, "stop")
	}
	if cmds[1].ID == nil || *cmds[1].ID != id {
		t.Errorf("command 1 id: got %v, want %d", cmds[1].ID, id)
	}
}

func TestManagerNextCommand(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	mock.waitForCommands(1, 2*time.Second)

	if err := mgr.Next(id); err != nil {
		t.Fatalf("Next: %v", err)
	}

	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	if cmds[1].Cmd != "next" {
		t.Errorf("command 1: got %q, want %q", cmds[1].Cmd, "next")
	}
}

func TestManagerUpdateCommand(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	mock.waitForCommands(1, 2*time.Second)

	if err := mgr.Update(id, `{"name":"Alice"}`); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	if cmds[1].Cmd != "update" {
		t.Errorf("command 1: got %q, want %q", cmds[1].Cmd, "update")
	}
	if cmds[1].Data != `{"name":"Alice"}` {
		t.Errorf("command 1 data: got %q, want %q", cmds[1].Data, `{"name":"Alice"}`)
	}

	// Verify LastData is stored.
	states := mgr.BrowserStates()
	if states[id].LastData != `{"name":"Alice"}` {
		t.Errorf("LastData: got %q, want %q", states[id].LastData, `{"name":"Alice"}`)
	}
}

// LoadURL is the one way a URL reaches the sidecar, so it refuses what
// CheckURL refuses even when a caller skipped the check.
func TestManagerLoadURLRefusesDisallowedURL(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}
	mock.waitForCommands(1, 2*time.Second)

	if err := mgr.LoadURL(id, "file:///etc/passwd"); !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("LoadURL: got %v, want ErrURLNotAllowed", err)
	}
	time.Sleep(50 * time.Millisecond)
	if cmds := mock.getCommands(); len(cmds) != 1 {
		t.Errorf("expected only the create command, got %+v", cmds)
	}
	if st := mgr.BrowserStates()[id]; st.URL != "" || st.Status != "allocated" {
		t.Errorf("state after refused load: URL=%q status=%q", st.URL, st.Status)
	}
}

func TestManagerCustomActionCommand(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	id, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	mock.waitForCommands(1, 2*time.Second)

	if err := mgr.CustomAction(id, "highlight", `{"color":"red"}`); err != nil {
		t.Fatalf("CustomAction: %v", err)
	}

	cmds := mock.waitForCommands(2, 2*time.Second)
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(cmds))
	}

	if cmds[1].Cmd != "custom" {
		t.Errorf("command 1: got %q, want %q", cmds[1].Cmd, "custom")
	}
	if cmds[1].Action != "highlight" {
		t.Errorf("command 1 action: got %q, want %q", cmds[1].Action, "highlight")
	}
	if cmds[1].Payload != `{"color":"red"}` {
		t.Errorf("command 1 payload: got %q, want %q", cmds[1].Payload, `{"color":"red"}`)
	}
}

func TestManagerClose(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()

	// Close should send shutdown command.
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Wait for shutdown command.
	cmds := mock.waitForCommands(1, 2*time.Second)
	found := false
	for _, cmd := range cmds {
		if cmd.Cmd == "shutdown" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no shutdown command found after Close()")
	}

	// Double close should not panic.
	if err := mgr.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}

func TestManagerOnStateChange(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	callCount := 0
	var callMu sync.Mutex
	mgr.OnStateChange(func() {
		callMu.Lock()
		callCount++
		callMu.Unlock()
	})

	// Allocate should trigger state change.
	_, err := mgr.AllocateBrowser(0)
	if err != nil {
		t.Fatalf("AllocateBrowser: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		callMu.Lock()
		c := callCount
		callMu.Unlock()
		if c > 0 {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}

	callMu.Lock()
	t.Errorf("onStateChange not called, callCount=%d", callCount)
	callMu.Unlock()
}

func TestManagerInvalidBrowserID(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	// Operations on invalid IDs should return errors.
	if err := mgr.LoadURL(-1, "http://example.com"); err == nil {
		t.Error("LoadURL(-1) should return error")
	}
	if err := mgr.LoadURL(100, "http://example.com"); err == nil {
		t.Error("LoadURL(100) should return error")
	}
	if err := mgr.Play(100); err == nil {
		t.Error("Play(100) should return error")
	}
	if err := mgr.Stop(-1); err == nil {
		t.Error("Stop(-1) should return error")
	}
	if err := mgr.Next(100); err == nil {
		t.Error("Next(100) should return error")
	}
	if err := mgr.Update(100, "data"); err == nil {
		t.Error("Update(100) should return error")
	}
	if err := mgr.CustomAction(100, "a", "b"); err == nil {
		t.Error("CustomAction(100) should return error")
	}

	// FreeBrowser on invalid IDs should be a no-op (no panic).
	mgr.FreeBrowser(-1)
	mgr.FreeBrowser(100)
}

func TestManagerConnectBadPath(t *testing.T) {
	comp := &mockCompositor{}
	cfg := DefaultConfig()
	mgr := NewManager(comp, cfg)

	// Try to connect to nonexistent sockets.
	err := mgr.connectSockets("/tmp/nonexistent-"+fmt.Sprintf("%d", os.Getpid())+"-ctrl.sock", "/tmp/nonexistent-frame.sock")
	if err == nil {
		t.Fatal("expected error connecting to nonexistent sockets")
		_ = mgr.Close()
	}
}

func TestManager_AliveReflectsProcessState(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()

	require.True(t, mgr.Alive(), "should be alive after start")

	_ = mgr.Close()
	require.False(t, mgr.Alive(), "should not be alive after close")
}

func TestManager_DetectsFrameSocketDrop(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()
	require.True(t, mgr.Alive())

	_ = mock.frameConn.Close()

	require.Eventually(t, func() bool {
		return !mgr.Alive()
	}, 2*time.Second, 50*time.Millisecond,
		"manager should detect frame socket drop")
}

func TestManager_DetectsControlSocketDrop(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()
	require.True(t, mgr.Alive())

	_ = mock.controlConn.Close()

	require.Eventually(t, func() bool {
		return !mgr.Alive()
	}, 2*time.Second, 50*time.Millisecond,
		"manager should detect control socket drop")
}

func TestManager_ResetsBrowserPoolOnRestart(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()

	browserID, err := mgr.AllocateBrowser(1)
	require.NoError(t, err)
	mock.waitForCommands(1, time.Second)

	states := mgr.BrowserStates()
	require.Equal(t, "allocated", states[browserID].Status)

	mgr.resetBrowserPool()

	states = mgr.BrowserStates()
	for i, bs := range states {
		assert.Equal(t, "empty", bs.Status, "slot %d should be empty", i)
		assert.Equal(t, -1, bs.LayerID, "slot %d layerID should be -1", i)
	}
}

func TestManager_RestartCountIncrementsOnSocketDrop(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()
	defer mock.close()

	require.True(t, mgr.Alive())

	// Simulate process exit by closing both sockets (triggers read loop exits).
	mock.close()

	require.Eventually(t, func() bool {
		return !mgr.Alive()
	}, 2*time.Second, 50*time.Millisecond)

	// Restart count should eventually increment (restart loop fires).
	require.Eventually(t, func() bool {
		return mgr.RestartCount() > 0
	}, 5*time.Second, 100*time.Millisecond,
		"restart should have been attempted")
}

func TestManager_ConcurrentSocketDropsDoNotRace(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()

	// Close both sockets simultaneously.
	_ = mock.frameConn.Close()
	_ = mock.controlConn.Close()

	// Should not panic or deadlock.
	time.Sleep(500 * time.Millisecond)
	require.False(t, mgr.Alive())
}

func TestManager_StaleReadLoopDoesNotTriggerRestart(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()

	require.True(t, mgr.Alive())

	// Manually bump generation to simulate a restart having occurred.
	// This makes the current read loops "stale".
	mgr.spawnGen.Add(1)

	// Close the sockets — the old read loops will exit.
	mock.close()

	// The read loops should see a generation mismatch and NOT trigger restart.
	time.Sleep(500 * time.Millisecond)

	// alive should be false because sockets are closed, but restartCh should
	// NOT have been signaled since the generation doesn't match.
	// We verify by checking restart count stays 0 (no restart loop firing).
	require.Equal(t, int64(0), mgr.RestartCount(),
		"stale read loops should not trigger restart")
}

func TestManager_RestartCountAccessor(t *testing.T) {
	comp := &mockCompositor{}
	cfg := DefaultConfig()
	mgr := NewManager(comp, cfg)
	defer func() { _ = mgr.Close() }()

	require.Equal(t, int64(0), mgr.RestartCount())
	mgr.restartCount.Add(1)
	require.Equal(t, int64(1), mgr.RestartCount())
}

func TestManager_CleansUpOldSocketDirOnRestart(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()

	// setupManagerWithMock uses connectSockets (not spawn), so socketDir is
	// not set. Create a temp dir and inject it to simulate a real sidecar dir.
	oldDir, err := os.MkdirTemp("", "hgfx-cleanup-*")
	require.NoError(t, err)

	mgr.mu.Lock()
	mgr.socketDir = oldDir
	mgr.mu.Unlock()

	require.DirExists(t, oldDir)

	// Trigger restart by closing sockets.
	mock.close()

	// Wait for restart to fire — the restart loop removes the old dir
	// before attempting to respawn (which will fail with mock sockets).
	require.Eventually(t, func() bool {
		return mgr.RestartCount() > 0
	}, 5*time.Second, 100*time.Millisecond,
		"restart should have been attempted")

	// Old directory should be cleaned up.
	require.NoDirExists(t, oldDir, "old socket dir should be removed on restart")
}

func TestManager_NotifiesStateChangeOnPoolReset(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	var called atomic.Int32
	mgr.OnStateChange(func() {
		called.Add(1)
	})

	before := called.Load()
	mgr.resetBrowserPool()

	require.Eventually(t, func() bool {
		return called.Load() > before
	}, time.Second, 50*time.Millisecond,
		"state change should fire on pool reset")
}

func TestManager_FullCrashRestartCycle(t *testing.T) {
	mgr, mock, _ := setupManagerWithMock(t)
	defer func() { _ = mgr.Close() }()

	// Step 1: Allocate and verify.
	browserID, err := mgr.AllocateBrowser(1)
	require.NoError(t, err)
	mock.waitForCommands(1, time.Second)

	states := mgr.BrowserStates()
	require.Equal(t, "allocated", states[browserID].Status)

	// Step 2: Kill sidecar.
	mock.close()

	// Step 3: Wait for detection.
	require.Eventually(t, func() bool {
		return !mgr.Alive()
	}, 2*time.Second, 50*time.Millisecond,
		"manager should detect sidecar death")

	// Step 4: Verify pool reset.
	states = mgr.BrowserStates()
	for i, bs := range states {
		assert.Equal(t, "empty", bs.Status,
			"slot %d should be empty after crash", i)
	}

	// Step 5: Verify restart was attempted.
	require.Eventually(t, func() bool {
		return mgr.RestartCount() > 0
	}, 5*time.Second, 100*time.Millisecond,
		"restart should have been attempted")
}
