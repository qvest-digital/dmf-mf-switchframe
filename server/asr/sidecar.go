//go:build !windows

package asr

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/asr/asripc"
	"github.com/zsiec/switchframe/server/asr/shmring"
)

// SidecarConfig configures the ASR sidecar subprocess.
type SidecarConfig struct {
	BinPath          string // path to switchframe-asr binary
	ShmName          string // POSIX shm name (e.g., "/switchframe-asr-session123")
	ModelDir         string // path to Parakeet TRT model directory
	WhisperModelsDir string // directory containing ggml-*.bin Whisper models
	DefaultBackend   string // default ASR backend (parakeet or whisper)
	Device           int    // GPU device ID
}

// SidecarBackend implements the Backend interface by spawning an ASR sidecar
// process, feeding it audio via shared memory, and reading transcripts over
// a Unix socket using the asripc protocol.
type SidecarBackend struct {
	cfg SidecarConfig

	mu       sync.Mutex
	cmd      *exec.Cmd
	shm      *shmring.Writer
	conn     net.Conn
	listener net.Listener
	waitDone chan struct{} // closed when cmd.Wait() returns (one per spawn generation)

	socketPath string
	socketDir  string

	onTranscript func([]TranscriptWord, bool)

	spawnGen  atomic.Int64
	alive     atomic.Bool
	restartCh chan string
	doneCh    chan struct{}
	closeOnce sync.Once

	// Cached status from sidecar.
	cachedModel             atomic.Pointer[string]
	cachedLatency           atomic.Int64
	cachedBackend           atomic.Pointer[string]
	cachedAvailableBackends atomic.Pointer[[]string]
	cachedModels            atomic.Pointer[[]string]
	cachedBackendConfig     atomic.Pointer[json.RawMessage]
}

// NewSidecarBackend creates a new SidecarBackend but does not start it.
func NewSidecarBackend(cfg SidecarConfig) (*SidecarBackend, error) {
	if cfg.BinPath == "" {
		return nil, fmt.Errorf("asr: sidecar binary path is required")
	}
	if cfg.ShmName == "" {
		return nil, fmt.Errorf("asr: shared memory name is required")
	}
	if len(cfg.ShmName) == 0 || cfg.ShmName[0] != '/' {
		return nil, fmt.Errorf("asr: shared memory name must start with '/'")
	}

	return &SidecarBackend{
		cfg:       cfg,
		restartCh: make(chan string, 1),
		doneCh:    make(chan struct{}),
	}, nil
}

// Name returns the active backend name from the sidecar.
// Falls back to "parakeet" if no status has been received yet.
// Name returns "sidecar" — the engine-level backend identifier.
// Use ActiveBackendName() for the sidecar's internal active backend name.
func (s *SidecarBackend) Name() string {
	return "sidecar"
}

// ActiveBackendName returns the sidecar's internal active backend name
// (e.g., "parakeet" or "whisper").
func (s *SidecarBackend) ActiveBackendName() string {
	if p := s.cachedBackend.Load(); p != nil {
		return *p
	}
	return "parakeet"
}

// Start begins the sidecar process and IPC communication.
func (s *SidecarBackend) Start(onTranscript func([]TranscriptWord, bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onTranscript = onTranscript

	if err := s.spawnLocked(); err != nil {
		return fmt.Errorf("asr: start sidecar: %w", err)
	}

	go s.restartLoop()

	return nil
}

// IngestAudio feeds raw PCM to the sidecar via shared memory. Lock-free hot path.
func (s *SidecarBackend) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	if !s.alive.Load() {
		return
	}
	s.mu.Lock()
	shm := s.shm
	s.mu.Unlock()
	if shm != nil {
		shm.Write(pcm, pts, sampleRate, channels)
	}
}

// Reset clears audio buffers in the sidecar.
func (s *SidecarBackend) Reset() {
	s.sendCommand("reset")
}

// Stop halts inference in the sidecar but keeps it running.
func (s *SidecarBackend) Stop() {
	s.sendCommand("stop")
}

// Close shuts down the sidecar and releases all resources.
func (s *SidecarBackend) Close() {
	s.closeOnce.Do(func() {
		close(s.doneCh)
		s.alive.Store(false)

		s.mu.Lock()
		defer s.mu.Unlock()

		// Send shutdown command.
		if s.conn != nil {
			_ = asripc.WriteMessage(s.conn, &asripc.Message{Cmd: "shutdown"})
		}

		// Wait for the process to exit (watchProcess owns cmd.Wait).
		if s.waitDone != nil {
			select {
			case <-s.waitDone:
				// watchProcess already reaped it.
			case <-time.After(2 * time.Second):
				// Force kill if it didn't exit in time.
				if s.cmd != nil && s.cmd.Process != nil {
					s.cmd.Process.Kill()
				}
				<-s.waitDone
			}
		}

		s.cleanupLocked()
	})
}

// Models lists available models for the active backend in the sidecar.
func (s *SidecarBackend) Models() []string {
	if p := s.cachedModels.Load(); p != nil {
		return *p
	}
	return []string{"parakeet-tdt-1.1b"}
}

// SetModel swaps the model at runtime via IPC to the sidecar.
func (s *SidecarBackend) SetModel(name string) error {
	cfg, _ := json.Marshal(map[string]string{"model": name})
	s.sendCommand("set_config", json.RawMessage(cfg))
	return nil
}

// ModelName returns the currently loaded model's display name.
func (s *SidecarBackend) ModelName() string {
	if p := s.cachedModel.Load(); p != nil {
		return *p
	}
	return "parakeet-tdt-1.1b"
}

// Config returns the backend-specific configuration as JSON.
func (s *SidecarBackend) Config() json.RawMessage {
	if p := s.cachedBackendConfig.Load(); p != nil {
		return *p
	}
	return json.RawMessage(`{}`)
}

// SetConfig applies backend-specific configuration from JSON.
func (s *SidecarBackend) SetConfig(raw json.RawMessage) error {
	s.sendCommand("set_config", raw)
	return nil
}

// spawnLocked starts the sidecar process. Must be called with s.mu held.
func (s *SidecarBackend) spawnLocked() error {
	gen := s.spawnGen.Add(1)

	// Create temp dir for socket.
	socketDir, err := os.MkdirTemp("", "switchframe-asr-*")
	if err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	s.socketDir = socketDir
	s.socketPath = filepath.Join(socketDir, "asr.sock")

	// Create shared memory writer.
	shm, err := shmring.NewWriter(s.cfg.ShmName, shmring.DefaultCapacity)
	if err != nil {
		os.RemoveAll(socketDir)
		return fmt.Errorf("create shm: %w", err)
	}
	s.shm = shm

	// Listen on Unix socket.
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("listen socket: %w", err)
	}
	s.listener = listener

	// Build sidecar args. Only pass optional flags when non-empty.
	args := []string{
		"--shm-name", s.cfg.ShmName,
		"--socket", s.socketPath,
		"--device", strconv.Itoa(s.cfg.Device),
	}
	if s.cfg.ModelDir != "" {
		args = append(args, "--model-dir", s.cfg.ModelDir)
	}
	if s.cfg.WhisperModelsDir != "" {
		args = append(args, "--whisper-models-dir", s.cfg.WhisperModelsDir)
	}
	if s.cfg.DefaultBackend != "" {
		args = append(args, "--default-backend", s.cfg.DefaultBackend)
	}

	// Spawn sidecar process.
	cmd := exec.Command(s.cfg.BinPath, args...)

	// Capture stderr with a prefixed logger.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Capture stdout to watch for READY line.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("start sidecar: %w", err)
	}
	s.cmd = cmd

	// Log stderr in background.
	go sidecarLogger(stderr)

	// Wait for READY on stdout with timeout.
	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "READY" {
				readyCh <- nil
				return
			}
			slog.Info("asr-sidecar stdout", "line", line)
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
			cmd.Process.Kill()
			cmd.Wait()
			listener.Close()
			shm.Close()
			os.RemoveAll(socketDir)
			return fmt.Errorf("wait for READY: %w", err)
		}
	case <-time.After(120 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("sidecar READY timeout after 120s")
	}

	// Accept connection from sidecar.
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
		s.conn = conn
	case err := <-acceptErrCh:
		cmd.Process.Kill()
		cmd.Wait()
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("accept socket: %w", err)
	case <-time.After(120 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		listener.Close()
		shm.Close()
		os.RemoveAll(socketDir)
		return fmt.Errorf("sidecar socket connect timeout after 10s")
	}

	// Create a channel that watchProcess will close when cmd.Wait() returns.
	waitDone := make(chan struct{})
	s.waitDone = waitDone
	s.alive.Store(true)

	go s.readLoop(gen)
	go s.watchProcess(cmd, gen, waitDone)

	return nil
}

// readLoop reads messages from the sidecar's Unix socket.
func (s *SidecarBackend) readLoop(gen int64) {
	for {
		select {
		case <-s.doneCh:
			return
		default:
		}

		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()

		if conn == nil {
			return
		}

		msg, err := asripc.ReadMessage(conn)
		if err != nil {
			select {
			case <-s.doneCh:
				return
			default:
			}
			if s.spawnGen.Load() == gen {
				slog.Warn("asr-sidecar: read error, triggering restart", "err", err)
				s.triggerRestart(fmt.Sprintf("read error: %v", err))
			}
			return
		}

		switch msg.Type {
		case "transcript":
			words := make([]TranscriptWord, len(msg.Words))
			for i, w := range msg.Words {
				words[i] = TranscriptWord{
					Text:       w.Text,
					Confidence: w.Confidence,
					GapBefore:  w.GapBefore,
				}
			}
			if s.onTranscript != nil {
				s.onTranscript(words, msg.IsFinal)
			}

		case "status":
			if msg.Model != "" {
				model := msg.Model
				s.cachedModel.Store(&model)
			}
			if msg.LatencyMs > 0 {
				s.cachedLatency.Store(int64(msg.LatencyMs))
			}
			if msg.Backend != "" {
				backend := msg.Backend
				s.cachedBackend.Store(&backend)
			}
			if len(msg.AvailableBackends) > 0 {
				ab := msg.AvailableBackends
				s.cachedAvailableBackends.Store(&ab)
			}
			if len(msg.Models) > 0 {
				models := msg.Models
				s.cachedModels.Store(&models)
			}
			if msg.BackendConfig != nil || msg.NoiseFloor != "" {
				cfgMap := make(map[string]interface{})
				for k, v := range msg.BackendConfig {
					cfgMap[k] = v
				}
				if msg.NoiseFloor != "" {
					cfgMap["noiseFloor"] = msg.NoiseFloor
				}
				raw, _ := json.Marshal(cfgMap)
				rawMsg := json.RawMessage(raw)
				s.cachedBackendConfig.Store(&rawMsg)
			}
			slog.Debug("asr-sidecar: status", "state", msg.State, "model", msg.Model,
				"backend", msg.Backend, "available", msg.AvailableBackends, "latency_ms", msg.LatencyMs)

		case "error":
			slog.Warn("asr-sidecar: error from sidecar", "error", msg.Error)
		}
	}
}

// watchProcess waits for the sidecar process to exit and triggers restart if needed.
// It is the sole owner of cmd.Wait() — no other goroutine may call it.
func (s *SidecarBackend) watchProcess(cmd *exec.Cmd, gen int64, waitDone chan struct{}) {
	err := cmd.Wait()
	close(waitDone)

	select {
	case <-s.doneCh:
		return
	default:
	}

	if s.spawnGen.Load() == gen {
		reason := "process exited"
		if err != nil {
			reason = fmt.Sprintf("process exited: %v", err)
		}
		slog.Warn("asr-sidecar: process exited unexpectedly", "err", err)
		s.triggerRestart(reason)
	}
}

// restartLoop handles automatic restarts with exponential backoff.
func (s *SidecarBackend) restartLoop() {
	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-s.doneCh:
			return
		case reason := <-s.restartCh:
			slog.Info("asr-sidecar: restarting", "reason", reason, "backoff", backoff)

			s.alive.Store(false)

			// Clean up old resources.
			s.mu.Lock()
			s.killAndCleanupLocked()
			s.mu.Unlock()

			// Wait with backoff.
			select {
			case <-s.doneCh:
				return
			case <-time.After(backoff):
			}

			// Attempt respawn.
			s.mu.Lock()
			err := s.spawnLocked()
			s.mu.Unlock()

			if err != nil {
				slog.Error("asr-sidecar: respawn failed", "err", err)
				backoff = min(backoff*2, maxBackoff)
				// Re-trigger restart to try again.
				s.triggerRestart(fmt.Sprintf("respawn failed: %v", err))
			} else {
				slog.Info("asr-sidecar: respawned successfully")
				backoff = 1 * time.Second
			}
		}
	}
}

// triggerRestart sends a non-blocking restart signal.
func (s *SidecarBackend) triggerRestart(reason string) {
	select {
	case s.restartCh <- reason:
	default:
		// Already a restart pending.
	}
}

// sendCommand sends a command to the sidecar over the Unix socket.
func (s *SidecarBackend) sendCommand(cmd string, config ...json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.alive.Load() || s.conn == nil {
		return
	}

	msg := &asripc.Message{Cmd: cmd}
	if len(config) > 0 {
		msg.Config = config[0]
	}

	if err := asripc.WriteMessage(s.conn, msg); err != nil {
		slog.Warn("asr-sidecar: send command failed", "cmd", cmd, "err", err)
	}
}

// killAndCleanupLocked kills the current process and cleans up resources.
// Must be called with s.mu held.
func (s *SidecarBackend) killAndCleanupLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		// Wait for watchProcess to reap the process.
		if s.waitDone != nil {
			<-s.waitDone
		}
	}
	s.cleanupLocked()
}

// cleanupLocked releases socket, listener, shm, and temp dir.
// Must be called with s.mu held.
func (s *SidecarBackend) cleanupLocked() {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
	if s.shm != nil {
		s.shm.Close()
		s.shm = nil
	}
	if s.socketDir != "" {
		os.RemoveAll(s.socketDir)
		s.socketDir = ""
		s.socketPath = ""
	}
	s.cmd = nil
	s.waitDone = nil
}

// sidecarLogger reads from the sidecar's stderr and logs each line.
func sidecarLogger(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		slog.Info("asr-sidecar", "msg", scanner.Text())
	}
}

// Compile-time interface check.
var _ Backend = (*SidecarBackend)(nil)
