package sidecar

import (
	"bufio"
	"context"
	"errors"
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

	"github.com/zsiec/switchframe/server/internal/backoff"
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

	doneCh    chan struct{}       // closed by Close()
	restartCh chan restartRequest // buffered cap 1
	closeOnce sync.Once

	// OnMessage is called for each message received from the sidecar.
	// It is called from the readLoop goroutine — callers must not block.
	OnMessage func(*Message)
}

// restartRequest is a restart signal tagged with the spawn generation that
// produced it, so restartLoop can tell stale signals from the generation it
// just replaced apart from a legitimate crash signal of the generation it
// just spawned.
type restartRequest struct {
	gen    int64
	reason string
}

// NewHost creates a Host with the given configuration. Call Start to spawn
// the sidecar process.
func NewHost(cfg Config) *Host {
	return &Host{
		cfg:       cfg.withDefaults(),
		doneCh:    make(chan struct{}),
		restartCh: make(chan restartRequest, 1),
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

	// Bound the write: a wedged sidecar (alive but no longer reading) with a
	// full socket send buffer would otherwise block here forever while
	// holding writeMu, permanently blocking every subsequent sender with no
	// recovery until the process exits.
	_ = conn.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout))
	err := WriteMessage(conn, msg)
	_ = conn.SetWriteDeadline(time.Time{})
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			// A timed-out control write means the sidecar is wedged; process
			// exit is not coming, so trigger recovery explicitly. The stream
			// may also be desynced by a partial write.
			h.triggerRestart(fmt.Sprintf("control write timeout: %v", err))
		}
		return err
	}
	return nil
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
			// Bound the write: a wedged sidecar (alive but not reading) with
			// a full socket send buffer would otherwise block this write
			// forever while h.mu is held, hanging the caller's shutdown and
			// deadlocking every other Host method behind the mutex. Setting
			// the deadline first also unblocks an in-flight Send stuck
			// writing to the wedged sidecar, so writeMu is acquired promptly.
			_ = h.conn.SetWriteDeadline(time.Now().Add(h.cfg.ShutdownTimeout))
			h.writeMu.Lock()
			// Re-arm the deadline after acquiring writeMu: an in-flight Send
			// that just timed out clears the deadline in its epilogue before
			// releasing writeMu, which would otherwise leave the shutdown
			// write below unbounded on a wedged conn.
			_ = h.conn.SetWriteDeadline(time.Now().Add(h.cfg.ShutdownTimeout))
			_ = WriteMessage(h.conn, &Message{Cmd: "shutdown"})
			h.writeMu.Unlock()
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
			_ = os.RemoveAll(socketDir)
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
	cmd.Env = h.cfg.Env
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
		_ = h.listener.Close()
		h.listener = nil
	}
	if h.conn != nil {
		_ = h.conn.Close()
		h.conn = nil
	}
	if h.socketDir != "" {
		_ = os.RemoveAll(h.socketDir)
		h.socketDir = ""
	}
	h.cmd = nil
}

// readLoop reads messages from the sidecar and dispatches to OnMessage.
func (h *Host) readLoop(gen int64) {
	defer func() {
		// If this generation is still current, trigger a restart.
		if h.spawnGen.Load() == gen {
			h.triggerRestartForGen(gen, "read loop ended")
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
		h.triggerRestartForGen(gen, reason)
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

// triggerRestart sends a non-blocking restart signal attributed to the
// current spawn generation.
func (h *Host) triggerRestart(reason string) {
	h.triggerRestartForGen(h.spawnGen.Load(), reason)
}

// triggerRestartForGen sends a non-blocking restart signal attributed to the
// given spawn generation.
func (h *Host) triggerRestartForGen(gen int64, reason string) {
	h.alive.Store(false)
	h.notifyStatus(reason)
	select {
	case h.restartCh <- restartRequest{gen: gen, reason: reason}:
	default:
		// Already a restart pending.
	}
}

// drainStaleRestartSignal removes a buffered restart signal left over from
// an old generation after a successful respawn. A signal from the current
// (just-spawned) generation is a legitimate crash report — e.g. a
// crash-looping binary that exits between spawnLocked returning and this
// drain — and must be kept, or the host would stay dead forever with no
// further restarts (the companion notifier's send was dropped while the
// buffer was full, so no second signal is coming).
func (h *Host) drainStaleRestartSignal() {
	select {
	case req := <-h.restartCh:
		if req.gen >= h.spawnGen.Load() {
			// Not stale: put it back. The channel has capacity 1 and this is
			// the only consumer, so the send only fails if a newer signal
			// just arrived — in which case dropping req still leaves a
			// restart pending.
			select {
			case h.restartCh <- req:
			default:
			}
		} else if !h.alive.Load() {
			// Stale signal discarded, but the current generation has already
			// failed: its triggerRestartForGen set alive=false yet dropped
			// its channel send because the stale signal filled the cap-1
			// buffer. spawnLocked set alive=true on success, so !alive here
			// means a post-spawn failure was signaled — recover it, or the
			// host would stay dead forever. A spurious recovered signal
			// racing Close is harmless: restartLoop's backoff select
			// observes doneCh and returns before respawning.
			select {
			case h.restartCh <- restartRequest{
				gen:    h.spawnGen.Load(),
				reason: "restart signal recovered after drain",
			}:
			default:
			}
		}
	default:
	}
}

// restartLoop handles automatic restarts with exponential backoff.
func (h *Host) restartLoop() {
	bo := backoff.Backoff{Initial: h.cfg.InitialBackoff, Max: h.cfg.MaxBackoff, JitterPct: 0.25}

	for {
		select {
		case <-h.doneCh:
			return
		case req := <-h.restartCh:
			slog.Info("sidecar: restarting", "reason", req.reason)

			h.alive.Store(false)

			// Kill old process and clean up.
			h.mu.Lock()
			h.killAndCleanupLocked()
			h.mu.Unlock()

			// Wait with backoff (interruptible by doneCh).
			wait := bo.Next()
			select {
			case <-h.doneCh:
				return
			case <-time.After(wait):
			}

			// Attempt respawn.
			h.mu.Lock()
			err := h.spawnLocked(context.Background())
			h.mu.Unlock()

			if err != nil {
				slog.Error("sidecar: respawn failed", "err", err)
				h.notifyStatus(fmt.Sprintf("respawn failed: %v", err))
				// Re-trigger to try again.
				h.triggerRestart(fmt.Sprintf("respawn failed: %v", err))
			} else {
				slog.Info("sidecar: respawned successfully")
				bo.Reset()
				h.restartCnt.Add(1)

				// Drain any stale restart signal from the old generation's
				// readLoop/watchProcess that arrived after restartLoop consumed
				// the original signal but before the new generation started.
				h.drainStaleRestartSignal()

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
	_ = h.cleanupLocked()
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
		_ = os.RemoveAll(h.socketDir)
		h.socketDir = ""
	}
	h.cmd = nil
	h.waitDone = nil

	return firstErr
}

// maxSidecarLogLine caps how many bytes of a single newline-less stderr line
// sidecarLogger will buffer. A wedged or crashing CEF/ASR child can emit a huge
// line (crash dump, binary garbage) with no '\n'; without a cap l.buf would grow
// without bound, consuming host memory proportional to untrusted child output.
const maxSidecarLogLine = 8 << 10 // 8 KiB

// sidecarLogger forwards sidecar stderr lines to slog.
// Write is serialized by a mutex since exec.Cmd does not guarantee
// single-writer semantics for Stderr.
type sidecarLogger struct {
	prefix    string
	mu        sync.Mutex
	buf       []byte
	truncated bool // dropping bytes of an over-long line until the next newline
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
			// No newline yet. If the buffered line has grown past the cap, flush
			// what we have with a truncation marker and discard the rest of the
			// line as it arrives, so an endless newline-less child cannot grow
			// l.buf without bound.
			if len(l.buf) > maxSidecarLogLine {
				if !l.truncated {
					line := strings.TrimSpace(string(l.buf[:maxSidecarLogLine]))
					if line != "" {
						slog.Info(l.prefix, "msg", line+" [line truncated]")
					}
				}
				l.buf = l.buf[:0]
				l.truncated = true
			}
			break
		}
		// Reached a newline. If we were mid-truncation, the remaining bytes up to
		// the newline are the tail of the discarded over-long line; drop them and
		// resume normal line handling on the next iteration.
		if l.truncated {
			l.buf = l.buf[idx+1:]
			l.truncated = false
			continue
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
