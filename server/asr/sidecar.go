//go:build !windows

package asr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/asr/asripc"
	"github.com/zsiec/switchframe/server/asr/shmring"
	"github.com/zsiec/switchframe/server/sidecar"
)

// SidecarConfig configures the ASR sidecar subprocess.
type SidecarConfig struct {
	BinPath          string // path to switchframe-asr binary
	ShmName          string // POSIX shm name (e.g., "/switchframe-asr-session123")
	ModelDir         string // path to Parakeet TRT model directory
	WhisperModelsDir string // directory containing ggml-*.bin Whisper models
	DefaultBackend   string // default ASR backend (parakeet or whisper)
	Device           int    // GPU device ID
	WhisperThreads   int    // whisper.cpp inference threads (0 = auto: min(NumCPU, 8))
}

// SidecarBackend implements the Backend interface by spawning an ASR sidecar
// process, feeding it audio via shared memory, and reading transcripts over
// a Unix socket using the asripc protocol.
//
// Lifecycle management (spawn, restart, shutdown) is delegated to sidecar.Host.
// The ASR backend uses DisableReadLoop because asripc.Message has a different
// schema than sidecar.Message — transcript/status fields are top-level, not
// nested in Data.
type SidecarBackend struct {
	cfg  SidecarConfig
	host *sidecar.Host

	mu      sync.Mutex // guards shm
	writeMu sync.Mutex // serializes writes to the control socket
	shm     *shmring.Writer

	onTranscript func([]TranscriptWord, bool)

	// Cached status from sidecar.
	cachedModel             atomic.Pointer[string]
	cachedLatency           atomic.Int64
	cachedBackend           atomic.Pointer[string]
	cachedAvailableBackends atomic.Pointer[[]string]
	cachedModels            atomic.Pointer[[]string]
	cachedBackendConfig     atomic.Pointer[json.RawMessage]

	// onStatusUpdate is called when the sidecar reports a status change
	// (backend switch, model change, config update). Used to refresh the
	// app-level ASR state cache and broadcast to the UI.
	onStatusUpdate atomic.Pointer[func()]
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
		cfg: cfg,
	}, nil
}

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

// OnStatusUpdate registers a callback invoked when the sidecar reports a
// status change (backend/model/config updates). Used by the app to refresh
// the ASR state cache and broadcast to the UI.
func (s *SidecarBackend) OnStatusUpdate(fn func()) {
	s.onStatusUpdate.Store(&fn)
}

// Start begins the sidecar process and IPC communication.
func (s *SidecarBackend) Start(onTranscript func([]TranscriptWord, bool)) error {
	s.onTranscript = onTranscript

	// Create the sidecar host with ASR-specific configuration.
	// DisableReadLoop is set because asripc.Message has a different schema
	// than sidecar.Message — we run our own readLoop using asripc.ReadMessage.
	host := sidecar.NewHost(sidecar.Config{
		Binary:          s.cfg.BinPath,
		ReadyTimeout:    120 * time.Second, // 120s for model loading
		DisableReadLoop: true,
		BuildArgs: func(socketDir string) []string {
			socketPath := filepath.Join(socketDir, "control.sock")
			args := []string{
				"--shm-name", s.cfg.ShmName,
				"--socket", socketPath,
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
			if s.cfg.WhisperThreads > 0 {
				args = append(args, "--whisper-threads", strconv.Itoa(s.cfg.WhisperThreads))
			}
			return args
		},
		OnRestart: func(gen int64) {
			// Start a new readLoop for the new connection. The shm writer
			// persists across restarts — the new sidecar process re-opens
			// the same named shared memory region. Destroying and recreating
			// it here would race with the sidecar (which opens it during init,
			// before printing READY).
			go s.readLoop(gen)
		},
	})
	s.host = host

	// Create shared memory writer before spawn.
	shm, err := shmring.NewWriter(s.cfg.ShmName, shmring.DefaultCapacity)
	if err != nil {
		return fmt.Errorf("asr: create shm: %w", err)
	}
	s.mu.Lock()
	s.shm = shm
	s.mu.Unlock()

	if err := host.Start(context.Background()); err != nil {
		s.mu.Lock()
		if s.shm != nil {
			_ = s.shm.Close()
			s.shm = nil
		}
		s.mu.Unlock()
		return fmt.Errorf("asr: start sidecar: %w", err)
	}

	// Start our own readLoop since DisableReadLoop is set.
	go s.readLoop(host.Generation())

	return nil
}

// testHookIngestBeforeShmWrite, when non-nil, is invoked by IngestAudio
// immediately before the shared-memory write. Test-only seam used to pin an
// ingest mid-write while Close runs concurrently.
var testHookIngestBeforeShmWrite func()

// IngestAudio feeds raw PCM to the sidecar via shared memory. Hot path.
//
// s.mu is held across the shm write: Close munmaps the segment under the same
// mutex, so an in-flight write can never touch unmapped memory. The only
// contention is with Start/Close (the write itself is a microsecond-scale
// memcpy), so this does not serialize against any other hot path.
func (s *SidecarBackend) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	if !s.host.Alive() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shm == nil {
		return
	}
	if h := testHookIngestBeforeShmWrite; h != nil {
		h()
	}
	s.shm.Write(pcm, pts, sampleRate, channels)
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
	if s.host != nil {
		_ = s.host.Close()
	}

	s.mu.Lock()
	if s.shm != nil {
		_ = s.shm.Close()
		s.shm = nil
	}
	s.mu.Unlock()
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

// readLoop reads messages from the sidecar's Unix socket using asripc protocol.
// Triggers restart via host.TriggerRestart on read errors.
func (s *SidecarBackend) readLoop(gen int64) {
	for {
		if !s.host.Alive() {
			return
		}

		conn := s.host.Conn()
		if conn == nil {
			return
		}

		msg, err := asripc.ReadMessage(conn)
		if err != nil {
			// Check if this is still the current generation before restarting.
			if s.host.Generation() == gen {
				slog.Warn("asr-sidecar: read error, triggering restart", "err", err)
				s.host.TriggerRestart(fmt.Sprintf("read error: %v", err))
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
			// Notify app to refresh state cache + broadcast to UI.
			if fn := s.onStatusUpdate.Load(); fn != nil {
				(*fn)()
			}

		case "error":
			slog.Warn("asr-sidecar: error from sidecar", "error", msg.Error)
		}
	}
}

// sendCommand sends a command to the sidecar over the Unix socket using asripc protocol.
// Writes are serialized by writeMu to prevent interleaved header+body from concurrent callers.
func (s *SidecarBackend) sendCommand(cmd string, config ...json.RawMessage) {
	if !s.host.Alive() {
		return
	}

	conn := s.host.Conn()
	if conn == nil {
		return
	}

	msg := &asripc.Message{Cmd: cmd}
	if len(config) > 0 {
		msg.Config = config[0]
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := asripc.WriteMessage(conn, msg); err != nil {
		slog.Warn("asr-sidecar: send command failed", "cmd", cmd, "err", err)
	}
}

// Compile-time interface check.
var _ Backend = (*SidecarBackend)(nil)
