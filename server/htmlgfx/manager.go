package htmlgfx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// compositorSetter is the interface the Manager needs from graphics.Compositor.
type compositorSetter interface {
	SetOverlay(id int, rgba []byte, width, height int, template string) error
}

// Manager manages a pool of browser instances in the CEF sidecar process.
// It communicates over Unix domain sockets using the JSON control protocol
// and binary frame protocol defined in protocol.go.
type Manager struct {
	compositor  compositorSetter
	config      Config
	controlConn net.Conn
	frameConn   net.Conn
	process     *os.Process
	socketDir   string // temp directory for Unix sockets

	mu        sync.Mutex
	browsers  []*BrowserState
	done      chan struct{}
	restartCh chan struct{} // buffered(1), signals restart loop
	stopOnce  sync.Once
	alive     atomic.Bool

	restartCount atomic.Int64
	spawnGen     atomic.Int64

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

// Start spawns the CEF sidecar process and connects to it.
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

	if err := m.spawn(binPath); err != nil {
		return err
	}

	go m.restartLoop()
	return nil
}

// spawn starts the sidecar process and connects sockets.
func (m *Manager) spawn(binPath string) error {
	gen := m.spawnGen.Add(1)

	// Create temp directory for Unix sockets.
	socketDir, err := os.MkdirTemp("", "switchframe-cef-*")
	if err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	m.mu.Lock()
	m.socketDir = socketDir
	m.mu.Unlock()

	controlPath := filepath.Join(socketDir, "control.sock")
	framePath := filepath.Join(socketDir, "frames.sock")

	// Use the socket dir as CEF's user data dir (unique per instance,
	// prevents singleton lock conflicts between concurrent sessions).
	userDataDir := filepath.Join(socketDir, "cef-data")

	// Spawn the sidecar.
	cmd := exec.Command(binPath,
		"--control-socket", controlPath,
		"--frame-socket", framePath,
		"--user-data-dir", userDataDir,
	)
	cmd.Stderr = &sidecarLogger{prefix: "cef-sidecar"}

	// Capture stdout to wait for READY signal.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Lock()
		m.socketDir = ""
		m.mu.Unlock()
		_ = os.RemoveAll(socketDir)
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		m.socketDir = ""
		m.mu.Unlock()
		_ = os.RemoveAll(socketDir)
		return fmt.Errorf("start sidecar: %w", err)
	}
	m.mu.Lock()
	m.process = cmd.Process
	m.mu.Unlock()

	slog.Info("htmlgfx: sidecar started", "pid", cmd.Process.Pid, "binary", binPath)

	// Wait for READY signal (with timeout).
	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "READY" {
				readyCh <- nil
				return
			}
		}
		if err := scanner.Err(); err != nil {
			readyCh <- fmt.Errorf("reading sidecar stdout: %w", err)
		} else {
			readyCh <- fmt.Errorf("sidecar exited before READY signal")
		}
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			m.killProcess()
			return err
		}
	case <-time.After(10 * time.Second):
		m.killProcess()
		return fmt.Errorf("sidecar did not send READY within 10s")
	}

	// Connect to the sidecar's sockets.
	if err := m.connectSockets(controlPath, framePath, gen); err != nil {
		m.killProcess()
		return fmt.Errorf("connect to sidecar: %w", err)
	}

	slog.Info("htmlgfx: sidecar connected", "control", controlPath, "frames", framePath)

	// Monitor for unexpected exit.
	go m.watchProcess(cmd)

	return nil
}

// watchProcess waits for the sidecar to exit and triggers restart if unexpected.
func (m *Manager) watchProcess(cmd *exec.Cmd) {
	err := cmd.Wait()

	select {
	case <-m.done:
		return // expected shutdown
	default:
	}

	slog.Error("htmlgfx: sidecar exited unexpectedly", "error", err)
	m.triggerRestart("process exited")
}

// killProcess terminates the sidecar and cleans up.
// It grabs m.mu to snapshot and nil-out process/socketDir, then
// performs blocking cleanup outside the lock.
func (m *Manager) killProcess() {
	m.mu.Lock()
	proc := m.process
	m.process = nil
	dir := m.socketDir
	m.socketDir = ""
	m.mu.Unlock()

	if proc != nil {
		_ = proc.Kill()
		_, _ = proc.Wait() // reap zombie
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// Alive returns whether the sidecar process is currently alive and connected.
// Unlike the old Started() flag, Alive becomes false when Close() is called
// or the sidecar process exits.
func (m *Manager) Alive() bool {
	return m.alive.Load()
}

// Started returns whether the sidecar is alive (backward-compatible wrapper).
func (m *Manager) Started() bool {
	return m.Alive()
}

// RestartCount returns the number of restart attempts made by the restart loop.
func (m *Manager) RestartCount() int64 {
	return m.restartCount.Load()
}

// SocketDir returns the current socket directory path. Returns "" if no
// sidecar is running.
func (m *Manager) SocketDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.socketDir
}

// triggerRestart signals that the sidecar needs restarting due to an
// unexpected socket closure or error. It sets alive to false and sends
// on restartCh (non-blocking). If the manager is already shutting down
// (done closed), this is a no-op.
func (m *Manager) triggerRestart(reason string) {
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

// restartLoop watches restartCh and respawns the sidecar with exponential backoff.
func (m *Manager) restartLoop() {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-m.done:
			return
		case <-m.restartCh:
		}

		// Kill existing process and clean up connections.
		m.killProcess()
		m.resetBrowserPool()

		m.mu.Lock()
		if m.controlConn != nil {
			m.controlConn.Close()
			m.controlConn = nil
		}
		if m.frameConn != nil {
			m.frameConn.Close()
			m.frameConn = nil
		}
		// Clean up old socket directory.
		oldDir := m.socketDir
		m.socketDir = ""
		m.mu.Unlock()

		if oldDir != "" {
			os.RemoveAll(oldDir)
		}

		slog.Info("htmlgfx: restarting sidecar", "backoff", backoff)

		select {
		case <-m.done:
			return
		case <-time.After(backoff):
		}

		if err := m.spawn(m.config.BinaryPath); err != nil {
			slog.Error("htmlgfx: restart failed", "error", err)
			backoff = min(backoff*2, maxBackoff)
		} else {
			slog.Info("htmlgfx: sidecar restarted successfully",
				"restart_count", m.restartCount.Load())
			backoff = time.Second // reset on success
		}
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
// and starts the read loop goroutines. The gen parameter is the spawn
// generation — read loops use it to avoid triggering restarts for stale
// connections after a new sidecar has already been spawned.
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
		proc := m.process
		m.process = nil
		dir := m.socketDir
		m.socketDir = ""
		m.mu.Unlock()

		// Send shutdown best-effort.
		if cc != nil {
			_ = WriteControlCommand(cc, ControlCommand{Cmd: "shutdown"})
		}

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

		// Give the sidecar 2s to exit gracefully, then kill.
		if proc != nil {
			done := make(chan struct{})
			go func() {
				_, _ = proc.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = proc.Kill()
				<-done
			}
		}

		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	})

	return closeErr
}

// sidecarLogger forwards sidecar stderr lines to slog.
type sidecarLogger struct {
	prefix string
	buf    []byte
}

func (l *sidecarLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		idx := -1
		for i, b := range l.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(l.buf[:idx]))
		l.buf = l.buf[idx+1:]
		if line != "" {
			slog.Info(l.prefix, "msg", line)
		}
	}
	return len(p), nil
}

// frameReadLoop reads frames from the frame connection and delivers them
// to the compositor. The gen parameter ties this loop to a specific spawn
// generation so that stale loops don't trigger restarts for a new sidecar.
func (m *Manager) frameReadLoop(gen int64) {
	defer func() {
		if m.spawnGen.Load() == gen {
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
		if m.spawnGen.Load() == gen {
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
