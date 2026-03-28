package sidecar

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Host manages the lifecycle of a sidecar process: spawning, ready handshake,
// message passing over a Unix socket, process monitoring, and automatic
// restart with exponential backoff and jitter.
type Host struct {
	cfg Config

	mu        sync.Mutex
	writeMu   sync.Mutex // serializes WriteMessage calls to prevent interleaved writes
	cmd       *exec.Cmd
	conn      net.Conn
	listener  net.Listener
	socketDir string
	waitDone  chan struct{} // closed when cmd.Wait() returns

	alive      atomic.Bool
	spawnGen   atomic.Int64
	restartCnt atomic.Int64

	doneCh    chan struct{} // closed by Close()
	restartCh chan string   // buffered cap 1
	closeOnce sync.Once

	// OnMessage is called for each message received from the sidecar.
	// It is called from the readLoop goroutine — callers must not block.
	OnMessage func(*Message)
}

// NewHost creates a Host with the given configuration. Call Start to spawn
// the sidecar process.
func NewHost(cfg Config) *Host {
	return &Host{
		cfg:       cfg.withDefaults(),
		doneCh:    make(chan struct{}),
		restartCh: make(chan string, 1),
	}
}

// Alive returns whether the sidecar process is running and connected.
func (h *Host) Alive() bool {
	return h.alive.Load()
}

// Generation returns the current spawn generation (incremented on each spawn).
func (h *Host) Generation() int64 {
	return h.spawnGen.Load()
}

// Restarts returns how many times the sidecar has been restarted.
func (h *Host) Restarts() int64 {
	return h.restartCnt.Load()
}

// Conn returns the control connection to the sidecar, or nil.
func (h *Host) Conn() net.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn
}

// SocketDir returns the current socket directory path. Returns "" if no
// sidecar is running. Useful for sidecars that create additional sockets
// beyond the control channel (e.g. frame sockets).
func (h *Host) SocketDir() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.socketDir
}

// TriggerRestart allows external code (e.g. custom read loops) to signal
// that the sidecar needs restarting. This is used when Config.Connect is
// set and the Host doesn't run its own readLoop.
func (h *Host) TriggerRestart(reason string) {
	h.triggerRestart(reason)
}

// Start spawns the sidecar process and starts the restart loop. The context
// is used only for the initial spawn — the restart loop runs independently.
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.spawnLocked(ctx); err != nil {
		return fmt.Errorf("sidecar: start: %w", err)
	}

	go h.restartLoop()
	return nil
}

// Send sends a message to the sidecar over the control socket.
// Safe for concurrent use — writes are serialized by writeMu.
func (h *Host) Send(msg *Message) error {
	if !h.alive.Load() {
		return fmt.Errorf("sidecar: not alive")
	}

	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("sidecar: no connection")
	}

	// Serialize writes to prevent interleaved header+body from concurrent callers.
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	return WriteMessage(conn, msg)
}

// Close performs a graceful shutdown of the sidecar. Safe to call multiple times.
func (h *Host) Close() error {
	var closeErr error

	h.closeOnce.Do(func() {
		h.alive.Store(false)
		close(h.doneCh)

		h.mu.Lock()
		defer h.mu.Unlock()

		// Send shutdown command best-effort. Skip when the caller handles
		// shutdown via a different protocol (e.g. CEF's newline-delimited JSON).
		if h.conn != nil && !h.cfg.DisableShutdownMessage {
			_ = WriteMessage(h.conn, &Message{Cmd: "shutdown"})
		}

		// Wait for the process to exit gracefully, then force-kill.
		if h.waitDone != nil {
			timeout := h.cfg.ShutdownTimeout
			select {
			case <-h.waitDone:
				// Process already exited.
			case <-time.After(timeout):
				if h.cmd != nil && h.cmd.Process != nil {
					_ = h.cmd.Process.Kill()
				}
				<-h.waitDone
			}
		}

		closeErr = h.cleanupLocked()
	})

	return closeErr
}

// spawnLocked starts the sidecar process and waits for the READY signal and
// socket connection. Must be called with h.mu held.
//
// Connection modes:
//   - Default (Connect nil): Host creates a Unix listener, passes socket path
//     as first arg, and waits for sidecar to connect. Host runs readLoop.
//   - Custom (Connect set): Host creates socket dir only, calls BuildArgs for
//     command args, and calls Connect after READY. Host does NOT run readLoop;
//     the caller handles reads and calls TriggerRestart on errors.
func (h *Host) spawnLocked(ctx context.Context) error {
	gen := h.spawnGen.Add(1)
	customConnect := h.cfg.Connect != nil

	// Create temp dir for the Unix socket.
	var socketDir string
	var err error
	if h.cfg.SocketDir != "" {
		socketDir, err = os.MkdirTemp(h.cfg.SocketDir, "sidecar-*")
	} else {
		socketDir, err = os.MkdirTemp("", "sidecar-*")
	}
	if err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	h.socketDir = socketDir

	// Default mode: create listener for sidecar to connect to.
	var listener net.Listener
	socketPath := filepath.Join(socketDir, "control.sock")
	if !customConnect {
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			os.RemoveAll(socketDir)
			h.socketDir = ""
			return fmt.Errorf("listen socket: %w", err)
		}
		h.listener = listener
	}

	// Build command arguments.
	var args []string
	if h.cfg.BuildArgs != nil {
		args = h.cfg.BuildArgs(socketDir)
	} else {
		args = append([]string{socketPath}, h.cfg.Args...)
	}
	cmd := exec.CommandContext(ctx, h.cfg.Binary, args...)
	cmd.Stderr = &sidecarLogger{prefix: "sidecar"}

	// Capture stdout for READY signal.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.abortSpawnLocked()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		h.abortSpawnLocked()
		return fmt.Errorf("start process: %w", err)
	}
	h.cmd = cmd

	slog.Info("sidecar: process started", "pid", cmd.Process.Pid, "binary", h.cfg.Binary)

	// Wait for READY on stdout.
	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "READY" {
				readyCh <- nil
				return
			}
			slog.Info("sidecar stdout", "line", line)
		}
		if err := scanner.Err(); err != nil {
			readyCh <- fmt.Errorf("reading stdout: %w", err)
		} else {
			readyCh <- fmt.Errorf("sidecar exited before READY")
		}
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			h.abortSpawnLocked()
			return fmt.Errorf("wait for READY: %w", err)
		}
	case <-time.After(h.cfg.ReadyTimeout):
		h.abortSpawnLocked()
		return fmt.Errorf("sidecar READY timeout after %v", h.cfg.ReadyTimeout)
	}

	// Establish control connection.
	if customConnect {
		// Custom: caller provides connection (e.g. dialing sidecar's listener).
		conn, err := h.cfg.Connect(socketDir)
		if err != nil {
			h.abortSpawnLocked()
			return fmt.Errorf("custom connect: %w", err)
		}
		h.conn = conn
	} else {
		// Default: accept connection from sidecar on our listener.
		acceptCh := make(chan net.Conn, 1)
		acceptErrCh := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				acceptErrCh <- err
				return
			}
			acceptCh <- conn
		}()

		select {
		case conn := <-acceptCh:
			h.conn = conn
		case err := <-acceptErrCh:
			h.abortSpawnLocked()
			return fmt.Errorf("accept socket: %w", err)
		case <-time.After(h.cfg.ReadyTimeout):
			h.abortSpawnLocked()
			return fmt.Errorf("sidecar socket connect timeout after %v", h.cfg.ReadyTimeout)
		}
	}

	// Process is up, socket connected.
	waitDone := make(chan struct{})
	h.waitDone = waitDone
	h.alive.Store(true)

	// Only run the built-in readLoop in default mode when not disabled.
	// In custom mode or DisableReadLoop mode, the caller runs their own
	// read loop and calls TriggerRestart on errors.
	if !customConnect && !h.cfg.DisableReadLoop {
		go h.readLoop(gen)
	}
	go h.watchProcess(cmd, gen, waitDone)

	h.notifyStatus("")
	return nil
}

// abortSpawnLocked kills the process and cleans up after a failed spawn.
// Must be called with h.mu held.
func (h *Host) abortSpawnLocked() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		_ = h.cmd.Wait()
	}
	if h.listener != nil {
		h.listener.Close()
		h.listener = nil
	}
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	if h.socketDir != "" {
		os.RemoveAll(h.socketDir)
		h.socketDir = ""
	}
	h.cmd = nil
}

// readLoop reads messages from the sidecar and dispatches to OnMessage.
func (h *Host) readLoop(gen int64) {
	defer func() {
		// If this generation is still current, trigger a restart.
		if h.spawnGen.Load() == gen {
			h.triggerRestart("read loop ended")
		}
	}()

	for {
		select {
		case <-h.doneCh:
			return
		default:
		}

		h.mu.Lock()
		conn := h.conn
		h.mu.Unlock()

		if conn == nil {
			return
		}

		msg, err := ReadMessage(conn)
		if err != nil {
			select {
			case <-h.doneCh:
				return
			default:
			}
			if h.spawnGen.Load() == gen {
				slog.Warn("sidecar: read error, triggering restart", "err", err)
			}
			return
		}

		if h.OnMessage != nil {
			h.OnMessage(msg)
		}
	}
}

// watchProcess waits for the sidecar process to exit and triggers restart if
// the exit was unexpected. It is the sole owner of cmd.Wait().
func (h *Host) watchProcess(cmd *exec.Cmd, gen int64, waitDone chan struct{}) {
	err := cmd.Wait()
	close(waitDone)

	select {
	case <-h.doneCh:
		return // expected shutdown
	default:
	}

	if h.spawnGen.Load() == gen {
		reason := "process exited"
		if err != nil {
			reason = fmt.Sprintf("process exited: %v", err)
		}
		slog.Warn("sidecar: process exited unexpectedly", "err", err)
		h.triggerRestart(reason)
	}
}

// notifyStatus calls the OnStatus callback if set.
func (h *Host) notifyStatus(lastErr string) {
	if h.cfg.OnStatus != nil {
		h.cfg.OnStatus(Status{
			Alive:      h.alive.Load(),
			Generation: h.spawnGen.Load(),
			Restarts:   h.restartCnt.Load(),
			LastError:  lastErr,
		})
	}
}

// triggerRestart sends a non-blocking restart signal.
func (h *Host) triggerRestart(reason string) {
	h.alive.Store(false)
	h.notifyStatus(reason)
	select {
	case h.restartCh <- reason:
	default:
		// Already a restart pending.
	}
}

// restartLoop handles automatic restarts with exponential backoff.
func (h *Host) restartLoop() {
	backoff := h.cfg.InitialBackoff

	for {
		select {
		case <-h.doneCh:
			return
		case reason := <-h.restartCh:
			slog.Info("sidecar: restarting", "reason", reason, "backoff", backoff)

			h.alive.Store(false)

			// Kill old process and clean up.
			h.mu.Lock()
			h.killAndCleanupLocked()
			h.mu.Unlock()

			// Wait with backoff (interruptible by doneCh).
			select {
			case <-h.doneCh:
				return
			case <-time.After(backoff):
			}

			// Attempt respawn.
			h.mu.Lock()
			err := h.spawnLocked(context.Background())
			h.mu.Unlock()

			if err != nil {
				slog.Error("sidecar: respawn failed", "err", err)
				backoff = min(backoff*2, h.cfg.MaxBackoff)
				// Add jitter: ±25% to prevent synchronized restart storms.
				jitter := backoff / 4
				if jitter > 0 {
					backoff += time.Duration(rand.Int64N(int64(jitter*2))) - jitter
				}
				h.notifyStatus(fmt.Sprintf("respawn failed: %v", err))
				// Re-trigger to try again.
				h.triggerRestart(fmt.Sprintf("respawn failed: %v", err))
			} else {
				slog.Info("sidecar: respawned successfully")
				backoff = h.cfg.InitialBackoff
				h.restartCnt.Add(1)

				// Drain any stale restart signals from the old generation's
				// readLoop/watchProcess that arrived after restartLoop consumed
				// the original signal but before the new generation started.
				select {
				case <-h.restartCh:
				default:
				}

				if h.cfg.OnRestart != nil {
					h.cfg.OnRestart(h.spawnGen.Load())
				}
			}
		}
	}
}

// killAndCleanupLocked kills the process and releases all resources.
// Must be called with h.mu held.
func (h *Host) killAndCleanupLocked() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		// Wait for watchProcess to reap, with a timeout to avoid
		// blocking indefinitely if Kill fails to deliver (zombie, etc.).
		if h.waitDone != nil {
			select {
			case <-h.waitDone:
			case <-time.After(5 * time.Second):
				slog.Warn("sidecar: timeout waiting for process to exit after kill")
			}
		}
	}
	h.cleanupLocked()
}

// cleanupLocked releases socket, listener, and temp dir.
// Must be called with h.mu held.
func (h *Host) cleanupLocked() error {
	var firstErr error

	if h.conn != nil {
		if err := h.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		h.conn = nil
	}
	if h.listener != nil {
		if err := h.listener.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		h.listener = nil
	}
	if h.socketDir != "" {
		os.RemoveAll(h.socketDir)
		h.socketDir = ""
	}
	h.cmd = nil
	h.waitDone = nil

	return firstErr
}

// sidecarLogger forwards sidecar stderr lines to slog.
// Write is serialized by a mutex since exec.Cmd does not guarantee
// single-writer semantics for Stderr.
type sidecarLogger struct {
	prefix string
	mu     sync.Mutex
	buf    []byte
}

func (l *sidecarLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

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

// Ensure sidecarLogger implements io.Writer.
var _ io.Writer = (*sidecarLogger)(nil)
