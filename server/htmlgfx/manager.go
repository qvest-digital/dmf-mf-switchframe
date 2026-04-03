package htmlgfx

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/backoff"
	"github.com/zsiec/switchframe/server/sidecar"
)

// compositorSetter is the interface the Manager needs from graphics.Compositor.
type compositorSetter interface {
	SetOverlay(id int, rgba []byte, width, height int, template string) error
}

// Manager manages a pool of browser instances in the CEF sidecar process.
// It communicates over Unix domain sockets using the JSON control protocol
// and binary frame protocol defined in protocol.go.
//
// Process lifecycle (spawn, restart, crash recovery) is delegated to
// sidecar.Host. The Manager owns the CEF-specific control and frame
// socket connections and read loops.
type Manager struct {
	compositor compositorSetter
	config     Config
	host       *sidecar.Host // nil until Start(); nil in test path (connectSockets)

	mu          sync.Mutex
	browsers    []*BrowserState
	controlConn net.Conn
	frameConn   net.Conn
	done        chan struct{} // closed on shutdown, checked by read loops
	stopOnce    sync.Once

	// Test path fields — used when connectSockets() is called directly
	// (tests) without going through Start()/Host. The Host handles all
	// of this in production.
	spawnGen     atomic.Int64
	alive        atomic.Bool
	restartCount atomic.Int64
	restartCh    chan struct{} // buffered(1), signals restart loop
	socketDir    string        // temp directory for Unix sockets (test path only)

	onStateChange func()
}

// NewManager creates a Manager with the given compositor and configuration.
// The browser pool is initialized with all slots in the "empty" state.
func NewManager(compositor compositorSetter, cfg Config) *Manager {
	browsers := make([]*BrowserState, cfg.PoolSize)
	for i := range browsers {
		bs := NewBrowserState(i)
		browsers[i] = &bs
	}

	return &Manager{
		compositor: compositor,
		config:     cfg,
		browsers:   browsers,
		done:       make(chan struct{}),
		restartCh:  make(chan struct{}, 1),
	}
}

// Start spawns the CEF sidecar process and connects to it using sidecar.Host.
// If the sidecar binary is not found, it logs a warning and returns nil
// (graceful degradation — HTML5 graphics won't be available but the server runs).
func (m *Manager) Start() error {
	binPath := m.config.BinaryPath
	if binPath == "" {
		slog.Info("htmlgfx: no sidecar binary path configured, HTML5 graphics disabled")
		return nil
	}

	// Resolve relative to the server binary's directory.
	if !filepath.IsAbs(binPath) {
		if execPath, err := os.Executable(); err == nil {
			binPath = filepath.Join(filepath.Dir(execPath), binPath)
		}
	}

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		slog.Warn("htmlgfx: sidecar binary not found, HTML5 graphics disabled", "path", binPath)
		return nil
	}

	h := sidecar.NewHost(sidecar.Config{
		Binary:                 binPath,
		ReadyTimeout:           10 * time.Second,
		DisableShutdownMessage: true, // Manager sends CEF-format shutdown in Close()
		BuildArgs: func(socketDir string) []string {
			controlPath := filepath.Join(socketDir, "control.sock")
			framePath := filepath.Join(socketDir, "frames.sock")
			userDataDir := filepath.Join(socketDir, "cef-data")
			return []string{
				"--control-socket", controlPath,
				"--frame-socket", framePath,
				"--user-data-dir", userDataDir,
			}
		},
		Connect: func(socketDir string) (net.Conn, error) {
			// CEF creates listeners, we connect to them.
			controlPath := filepath.Join(socketDir, "control.sock")
			var conn net.Conn
			var err error
			for i := 0; i < 50; i++ {
				conn, err = net.Dial("unix", controlPath)
				if err == nil {
					return conn, nil
				}
				time.Sleep(20 * time.Millisecond)
			}
			return nil, fmt.Errorf("connect control socket: %w", err)
		},
		OnRestart: func(gen int64) {
			m.resetBrowserPool()
			m.connectAfterSpawn()
		},
	})

	m.host = h

	if err := h.Start(context.Background()); err != nil {
		return err
	}

	// Connect the frame socket and start read loops.
	m.connectAfterSpawn()
	return nil
}

// connectAfterSpawn connects the frame socket and starts the control and
// frame read loops. Called after the Host has spawned the sidecar and
// established the control connection, both on initial start and on restart.
func (m *Manager) connectAfterSpawn() {
	socketDir := m.host.SocketDir()
	if socketDir == "" {
		return
	}
	framePath := filepath.Join(socketDir, "frames.sock")

	// Connect to the frame socket with retry.
	var frameConn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		frameConn, err = net.Dial("unix", framePath)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		slog.Error("htmlgfx: failed to connect frame socket", "error", err)
		m.host.TriggerRestart("frame socket connect failed")
		return
	}

	// Get the control connection from the Host.
	controlConn := m.host.Conn()
	if controlConn == nil {
		_ = frameConn.Close()
		slog.Error("htmlgfx: no control connection from host")
		m.host.TriggerRestart("no control connection")
		return
	}

	gen := m.host.Generation()

	m.mu.Lock()
	// Close any old connections from a previous generation.
	if m.frameConn != nil {
		_ = m.frameConn.Close()
	}
	m.controlConn = controlConn
	m.frameConn = frameConn
	m.mu.Unlock()

	slog.Info("htmlgfx: sidecar connected",
		"control", filepath.Join(socketDir, "control.sock"),
		"frames", framePath)

	go m.frameReadLoop(gen)
	go m.controlReadLoop(gen)
}

// Alive returns whether the sidecar process is currently alive and connected.
func (m *Manager) Alive() bool {
	if m.host != nil {
		return m.host.Alive()
	}
	// Test path: use the local alive flag.
	return m.alive.Load()
}

// Started returns whether the sidecar is alive (backward-compatible wrapper).
func (m *Manager) Started() bool {
	return m.Alive()
}

// RestartCount returns the number of restart attempts made by the restart loop.
func (m *Manager) RestartCount() int64 {
	if m.host != nil {
		return m.host.Restarts()
	}
	// Test path: use local counter.
	return m.restartCount.Load()
}

// SocketDir returns the current socket directory path. Returns "" if no
// sidecar is running.
func (m *Manager) SocketDir() string {
	if m.host != nil {
		return m.host.SocketDir()
	}
	// Test path: use local field.
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.socketDir
}

// triggerRestart signals that the sidecar needs restarting. If a Host is
// present, it delegates to the Host. Otherwise (test path), it uses the
// local restartCh and alive flag.
func (m *Manager) triggerRestart(reason string) {
	if m.host != nil {
		m.host.TriggerRestart(reason)
		return
	}
	// Test path: signal via local channels.
	select {
	case <-m.done:
		return // intentional shutdown
	default:
	}
	slog.Warn("htmlgfx: triggering restart", "reason", reason)
	m.alive.Store(false)
	select {
	case m.restartCh <- struct{}{}:
	default: // already pending
	}
}

// restartLoop watches restartCh and handles restart bookkeeping for the
// test path (when no Host is present). In production, the Host's restart
// loop handles all of this.
func (m *Manager) restartLoop() {
	bo := backoff.Backoff{}

	for {
		select {
		case <-m.done:
			return
		case <-m.restartCh:
		}

		// Clean up connections.
		m.resetBrowserPool()

		m.mu.Lock()
		if m.controlConn != nil {
			_ = m.controlConn.Close()
			m.controlConn = nil
		}
		if m.frameConn != nil {
			_ = m.frameConn.Close()
			m.frameConn = nil
		}
		// Clean up old socket directory.
		oldDir := m.socketDir
		m.socketDir = ""
		m.mu.Unlock()

		if oldDir != "" {
			_ = os.RemoveAll(oldDir)
		}

		wait := bo.Next()
		slog.Info("htmlgfx: restarting sidecar", "backoff", wait)

		select {
		case <-m.done:
			return
		case <-time.After(wait):
		}

		// In test path, spawn will fail (no binary). Just track the attempt.
		slog.Error("htmlgfx: restart failed (test path, no binary)")
		m.restartCount.Add(1)
	}
}

// resetBrowserPool resets all browser slots to empty and clears any
// compositor overlays they were driving. Called before restarting the sidecar.
func (m *Manager) resetBrowserPool() {
	m.mu.Lock()
	for i := range m.browsers {
		if m.browsers[i].Status != "empty" {
			if m.browsers[i].LayerID >= 0 && m.compositor != nil {
				_ = m.compositor.SetOverlay(m.browsers[i].LayerID, nil, 0, 0, "")
			}
			*m.browsers[i] = NewBrowserState(i)
		}
	}
	m.mu.Unlock()
	m.notifyStateChange()
	slog.Info("htmlgfx: browser pool reset (all slots empty)")
}

// connectSockets connects the manager to the sidecar's Unix domain sockets
// and starts the read loop goroutines. This is used by tests that bypass
// the Host and connect directly to a mock sidecar. The gen parameter is
// the spawn generation — read loops use it to avoid triggering restarts
// for stale connections after a new sidecar has already been spawned.
func (m *Manager) connectSockets(controlPath, framePath string, gen ...int64) error {
	controlConn, err := net.Dial("unix", controlPath)
	if err != nil {
		return fmt.Errorf("connect control socket: %w", err)
	}

	frameConn, err := net.Dial("unix", framePath)
	if err != nil {
		_ = controlConn.Close()
		return fmt.Errorf("connect frame socket: %w", err)
	}

	m.mu.Lock()
	m.controlConn = controlConn
	m.frameConn = frameConn
	m.mu.Unlock()
	m.alive.Store(true)

	// Use the provided generation or read current.
	g := m.spawnGen.Load()
	if len(gen) > 0 {
		g = gen[0]
	}

	go m.frameReadLoop(g)
	go m.controlReadLoop(g)

	return nil
}

// AllocateBrowser finds the first empty slot in the pool, marks it as allocated,
// assigns the given layer ID, and sends a "create" command to the sidecar.
// Returns the browser ID or an error if the pool is full.
func (m *Manager) AllocateBrowser(layerID int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slotID := -1
	for i, bs := range m.browsers {
		if bs.Status == "empty" {
			bs.Status = "allocated"
			bs.LayerID = layerID
			bs.Width = m.config.Width
			bs.Height = m.config.Height
			slotID = i
			break
		}
	}

	if slotID < 0 {
		return 0, fmt.Errorf("browser pool exhausted (pool size: %d)", m.config.PoolSize)
	}

	if err := m.sendCommand(ControlCommand{
		Cmd:    "create",
		ID:     intPtrHelper(slotID),
		Width:  m.config.Width,
		Height: m.config.Height,
		FPS:    m.config.FPS,
	}); err != nil {
		// Roll back on send failure.
		m.browsers[slotID].Status = "empty"
		m.browsers[slotID].LayerID = -1
		return 0, fmt.Errorf("send create command: %w", err)
	}

	m.notifyStateChange()
	return slotID, nil
}

// FreeBrowser sends a "destroy" command and resets the browser state to empty.
func (m *Manager) FreeBrowser(browserID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if browserID < 0 || browserID >= len(m.browsers) {
		return
	}

	bs := m.browsers[browserID]
	if bs.Status == "empty" {
		return
	}

	// Send destroy command best-effort.
	_ = m.sendCommand(ControlCommand{
		Cmd: "destroy",
		ID:  intPtrHelper(browserID),
	})

	// Reset to empty.
	*bs = NewBrowserState(browserID)

	m.notifyStateChange()
}

// LoadURL sets the URL on a browser and sends a "load" command.
func (m *Manager) LoadURL(browserID int, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	prevURL := m.browsers[browserID].URL
	prevStatus := m.browsers[browserID].Status

	m.browsers[browserID].URL = url
	m.browsers[browserID].Status = "loading"

	m.notifyStateChange()

	if err := m.sendCommand(ControlCommand{
		Cmd: "load",
		ID:  intPtrHelper(browserID),
		URL: url,
	}); err != nil {
		// Roll back state on send failure.
		m.browsers[browserID].URL = prevURL
		m.browsers[browserID].Status = prevStatus
		m.notifyStateChange()
		return err
	}
	return nil
}

// Play sends a "play" command for the given browser.
func (m *Manager) Play(browserID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	prevStatus := m.browsers[browserID].Status

	m.browsers[browserID].Status = "playing"

	m.notifyStateChange()

	if err := m.sendCommand(ControlCommand{
		Cmd: "play",
		ID:  intPtrHelper(browserID),
	}); err != nil {
		// Roll back state on send failure.
		m.browsers[browserID].Status = prevStatus
		m.notifyStateChange()
		return err
	}
	return nil
}

// Stop sends a "stop" command for the given browser.
func (m *Manager) Stop(browserID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	prevStatus := m.browsers[browserID].Status

	m.browsers[browserID].Status = "stopped"

	m.notifyStateChange()

	if err := m.sendCommand(ControlCommand{
		Cmd: "stop",
		ID:  intPtrHelper(browserID),
	}); err != nil {
		// Roll back state on send failure.
		m.browsers[browserID].Status = prevStatus
		m.notifyStateChange()
		return err
	}
	return nil
}

// Next sends a "next" command for the given browser.
func (m *Manager) Next(browserID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	return m.sendCommand(ControlCommand{
		Cmd: "next",
		ID:  intPtrHelper(browserID),
	})
}

// Update sends an "update" command with the given data JSON string.
func (m *Manager) Update(browserID int, data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	prevData := m.browsers[browserID].LastData

	m.browsers[browserID].LastData = data

	m.notifyStateChange()

	if err := m.sendCommand(ControlCommand{
		Cmd:  "update",
		ID:   intPtrHelper(browserID),
		Data: data,
	}); err != nil {
		// Roll back state on send failure.
		m.browsers[browserID].LastData = prevData
		m.notifyStateChange()
		return err
	}
	return nil
}

// CustomAction sends a "custom" command with the given action and payload.
func (m *Manager) CustomAction(browserID int, action, payload string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateBrowserID(browserID); err != nil {
		return err
	}

	return m.sendCommand(ControlCommand{
		Cmd:     "custom",
		ID:      intPtrHelper(browserID),
		Action:  action,
		Payload: payload,
	})
}

// BrowserStates returns a snapshot copy of all browser states.
func (m *Manager) BrowserStates() []BrowserState {
	m.mu.Lock()
	defer m.mu.Unlock()

	states := make([]BrowserState, len(m.browsers))
	for i, bs := range m.browsers {
		states[i] = *bs
	}
	return states
}

// BrowserForLayer returns the browser ID mapped to the given layer ID,
// or -1 if no browser is mapped to that layer.
func (m *Manager) BrowserForLayer(layerID int) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, bs := range m.browsers {
		if bs.LayerID == layerID && bs.Status != "empty" {
			return bs.ID
		}
	}
	return -1
}

// OnStateChange registers a callback that is invoked whenever browser
// state changes. The callback runs asynchronously in a goroutine and
// may safely call Manager methods like BrowserStates().
func (m *Manager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// Close sends a "shutdown" command, closes connections, and kills the sidecar.
// It is safe to call multiple times.
func (m *Manager) Close() error {
	var closeErr error

	m.stopOnce.Do(func() {
		m.alive.Store(false)
		close(m.done)

		// Snapshot and nil-out connection state under the lock.
		m.mu.Lock()
		cc := m.controlConn
		m.controlConn = nil
		fc := m.frameConn
		m.frameConn = nil
		m.mu.Unlock()

		// Send shutdown best-effort.
		if cc != nil {
			_ = WriteControlCommand(cc, ControlCommand{Cmd: "shutdown"})
		}

		// If using the Host (production path), delegate process shutdown.
		if m.host != nil {
			if cc != nil {
				_ = cc.Close()
			}
			if fc != nil {
				_ = fc.Close()
			}
			closeErr = m.host.Close()
			return
		}

		// Test path: close connections directly.
		if cc != nil {
			if err := cc.Close(); err != nil {
				closeErr = err
			}
		}
		if fc != nil {
			if err := fc.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})

	return closeErr
}

// frameReadLoop reads frames from the frame connection and delivers them
// to the compositor. The gen parameter ties this loop to a specific spawn
// generation so that stale loops don't trigger restarts for a new sidecar.
func (m *Manager) frameReadLoop(gen int64) {
	defer func() {
		if m.currentGen() == gen {
			m.triggerRestart("frame socket closed")
		}
	}()

	m.mu.Lock()
	conn := m.frameConn
	m.mu.Unlock()
	if conn == nil {
		return
	}

	for {
		select {
		case <-m.done:
			return
		default:
		}

		frame, err := ReadFrame(conn)
		if err == nil {
			slog.Info("htmlgfx: frame received", "browser", frame.BrowserID, "size", fmt.Sprintf("%dx%d", frame.Width, frame.Height))
		}
		if err != nil {
			select {
			case <-m.done:
				return
			default:
			}
			// Any read error (EOF, closed conn, protocol error) means
			// this connection is dead. Exit and let the defer handle restart.
			if err != io.EOF {
				slog.Warn("htmlgfx: frame read error", "error", err)
			}
			return
		}

		// Look up the layer ID for this browser.
		m.mu.Lock()
		browserID := int(frame.BrowserID)
		if browserID < 0 || browserID >= len(m.browsers) {
			m.mu.Unlock()
			continue
		}
		layerID := m.browsers[browserID].LayerID
		m.mu.Unlock()

		if layerID < 0 {
			continue
		}

		// Convert BGRA to RGBA in-place.
		bgraToRGBA(frame.Pixels, frame.Pixels)

		// Deliver to compositor.
		if err := m.compositor.SetOverlay(layerID, frame.Pixels, int(frame.Width), int(frame.Height), "html5"); err != nil {
			slog.Warn("htmlgfx: compositor SetOverlay failed", "layer", layerID, "error", err)
		}
	}
}

// controlReadLoop reads control responses from the sidecar and updates
// browser state accordingly. It uses a single bufio.Scanner to avoid
// the data-loss issue with ReadControlResponse creating a new scanner
// per call. The gen parameter ties this loop to a specific spawn generation.
func (m *Manager) controlReadLoop(gen int64) {
	defer func() {
		if m.currentGen() == gen {
			m.triggerRestart("control socket closed")
		}
	}()

	m.mu.Lock()
	conn := m.controlConn
	m.mu.Unlock()
	if conn == nil {
		return
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		select {
		case <-m.done:
			return
		default:
		}

		var resp ControlResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			slog.Debug("htmlgfx: control response unmarshal error", "error", err)
			continue
		}

		m.mu.Lock()
		if resp.ID >= 0 && resp.ID < len(m.browsers) {
			bs := m.browsers[resp.ID]
			switch resp.Event {
			case "format_detected":
				bs.Format = resp.Format
				bs.Status = "ready"
			case "load_error":
				bs.Status = "error"
			case "stopped":
				bs.Status = "stopped"
			}
			m.notifyStateChange()
		}
		m.mu.Unlock()
	}
}

// currentGen returns the current spawn generation. Uses Host.Generation()
// if a Host is present, otherwise uses the local spawnGen counter (test path).
func (m *Manager) currentGen() int64 {
	if m.host != nil {
		return m.host.Generation()
	}
	return m.spawnGen.Load()
}

// sendCommand writes a control command to the control connection.
// The caller must hold m.mu.
func (m *Manager) sendCommand(cmd ControlCommand) error {
	if m.controlConn == nil {
		return fmt.Errorf("control connection not established")
	}
	return WriteControlCommand(m.controlConn, cmd)
}

// validateBrowserID checks that the browser ID is within the valid range.
func (m *Manager) validateBrowserID(browserID int) error {
	if browserID < 0 || browserID >= len(m.browsers) {
		return fmt.Errorf("invalid browser ID: %d (pool size: %d)", browserID, len(m.browsers))
	}
	return nil
}

// notifyStateChange calls the onStateChange callback if set.
// Safe to call with or without the lock held — the callback runs
// asynchronously to avoid deadlocks (the callback may call BrowserStates
// which acquires the lock).
func (m *Manager) notifyStateChange() {
	fn := m.onStateChange
	if fn != nil {
		go fn()
	}
}

// intPtrHelper returns a pointer to the given int.
func intPtrHelper(i int) *int {
	return &i
}
