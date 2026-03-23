package htmlgfx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// compositorSetter is the interface the Manager needs from graphics.Compositor.
type compositorSetter interface {
	SetOverlay(id int, rgba []byte, width, height int, template string) error
}

// Manager manages a pool of browser instances in the CEF sidecar process.
// It communicates over Unix domain sockets using the JSON control protocol
// and binary frame protocol defined in protocol.go.
type Manager struct {
	compositor compositorSetter
	config     Config
	controlConn net.Conn
	frameConn   net.Conn

	mu       sync.Mutex
	browsers []*BrowserState
	done     chan struct{}
	stopOnce sync.Once

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
	}
}

// connectSockets connects the manager to the sidecar's Unix domain sockets
// and starts the read loop goroutines.
func (m *Manager) connectSockets(controlPath, framePath string) error {
	controlConn, err := net.Dial("unix", controlPath)
	if err != nil {
		return fmt.Errorf("connect control socket: %w", err)
	}

	frameConn, err := net.Dial("unix", framePath)
	if err != nil {
		_ = controlConn.Close()
		return fmt.Errorf("connect frame socket: %w", err)
	}

	m.controlConn = controlConn
	m.frameConn = frameConn

	go m.frameReadLoop()
	go m.controlReadLoop()

	return nil
}

// AllocateBrowser finds the first empty slot in the pool, marks it as allocated,
// assigns the given layer ID, and sends a "create" command to the sidecar.
// Returns the browser ID or an error if the pool is full.
func (m *Manager) AllocateBrowser(layerID int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, bs := range m.browsers {
		if bs.Status == "empty" {
			bs.Status = "allocated"
			bs.LayerID = layerID
			bs.Width = m.config.Width
			bs.Height = m.config.Height

			m.notifyStateChange()

			// Send create command (under lock is fine since write is fast).
			if err := m.sendCommand(ControlCommand{
				Cmd:    "create",
				ID:     intPtrHelper(i),
				Width:  m.config.Width,
				Height: m.config.Height,
				FPS:    m.config.FPS,
			}); err != nil {
				// Roll back on send failure.
				bs.Status = "empty"
				bs.LayerID = -1
				return 0, fmt.Errorf("send create command: %w", err)
			}

			return i, nil
		}
	}

	return 0, fmt.Errorf("browser pool exhausted (pool size: %d)", m.config.PoolSize)
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
// state changes. The callback is called with the lock held, so it must
// not call back into the Manager.
func (m *Manager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// Close sends a "shutdown" command and closes all connections.
// It is safe to call multiple times.
func (m *Manager) Close() error {
	var closeErr error

	m.stopOnce.Do(func() {
		close(m.done)

		// Send shutdown best-effort.
		if m.controlConn != nil {
			_ = m.sendCommand(ControlCommand{Cmd: "shutdown"})
		}

		if m.controlConn != nil {
			if err := m.controlConn.Close(); err != nil {
				closeErr = err
			}
		}
		if m.frameConn != nil {
			if err := m.frameConn.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})

	return closeErr
}

// frameReadLoop reads frames from the frame connection and delivers them
// to the compositor.
func (m *Manager) frameReadLoop() {
	for {
		select {
		case <-m.done:
			return
		default:
		}

		frame, err := ReadFrame(m.frameConn)
		if err != nil {
			select {
			case <-m.done:
				return
			default:
			}
			if err == io.EOF {
				return
			}
			// Log and continue on transient errors.
			slog.Warn("htmlgfx: frame read error", "error", err)
			continue
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
// per call.
func (m *Manager) controlReadLoop() {
	scanner := bufio.NewScanner(m.controlConn)
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
// The caller must hold m.mu or ensure thread safety.
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
// Must be called with m.mu held.
func (m *Manager) notifyStateChange() {
	if m.onStateChange != nil {
		m.onStateChange()
	}
}

// intPtrHelper returns a pointer to the given int.
func intPtrHelper(i int) *int {
	return &i
}
