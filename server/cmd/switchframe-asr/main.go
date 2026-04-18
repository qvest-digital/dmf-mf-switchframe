package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/asr/asripc"
	"github.com/zsiec/switchframe/server/asr/shmring"
)

func noiseFloorToDB(preset string) (float64, bool) {
	switch preset {
	case "quiet":
		return -35, true
	case "normal":
		return -26, true
	case "noisy":
		return -20, true
	default:
		return 0, false
	}
}

func dbToNoiseFloor(db float64) string {
	if db <= -30 {
		return "quiet"
	}
	if db <= -23 {
		return "normal"
	}
	return "noisy"
}

func emitWords(conn net.Conn, words []asr.TranscriptWord) {
	if len(words) == 0 {
		return
	}
	ipcWords := make([]asripc.Word, len(words))
	for i, w := range words {
		ipcWords[i] = asripc.Word{
			Text:       w.Text,
			Confidence: w.Confidence,
			GapBefore:  w.GapBefore,
		}
	}
	if err := asripc.WriteMessage(conn, &asripc.Message{
		Type:    "transcript",
		Words:   ipcWords,
		IsFinal: true,
	}); err != nil {
		slog.Warn("failed to write transcript", "error", err)
	}
}

// sendStatus sends a status message with backend/model info over IPC.
func sendStatus(conn net.Conn, active asrBackend, availableNames []string, eVAD *energyVAD) {
	msg := &asripc.Message{
		Type:              "status",
		State:             "ready",
		AvailableBackends: availableNames,
	}
	if active != nil {
		msg.Backend = active.Name()
		msg.Model = active.ModelName()
		msg.Models = active.Models()
		msg.BackendConfig = active.Config()
	}
	msg.NoiseFloor = dbToNoiseFloor(eVAD.thresholdDB())
	if err := asripc.WriteMessage(conn, msg); err != nil {
		slog.Warn("failed to send status", "error", err)
	}
}

func main() {
	shmName := flag.String("shm-name", "", "POSIX shared memory name")
	socketPath := flag.String("socket", "", "Unix socket path")
	modelDir := flag.String("model-dir", "", "Parakeet TRT model directory path")
	whisperModelsDir := flag.String("whisper-models-dir", "", "directory containing ggml-*.bin Whisper models")
	defaultBackend := flag.String("default-backend", "parakeet", "default ASR backend")
	device := flag.Int("device", 0, "GPU device ID")
	energyThreshold := flag.Float64("energy-threshold", 0.005, "RMS energy gate threshold")
	vadModelPath := flag.String("vad-model", "", "Silero VAD ONNX model path (auto-detected from model-dir)")
	whisperThreads := flag.Int("whisper-threads", 0, "Whisper inference thread count (0 = auto: min(NumCPU, 8))")
	buildEngines := flag.Bool("build-engines", false, "Build TRT engine plans from ONNX models and exit")
	flag.Parse()

	if *buildEngines {
		if *modelDir == "" {
			fmt.Fprintf(os.Stderr, "error: --model-dir is required with --build-engines\n")
			os.Exit(1)
		}
		if err := buildTRTEngines(*modelDir, *device); err != nil {
			slog.Error("engine build failed", "error", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *shmName == "" || *socketPath == "" {
		fmt.Fprintf(os.Stderr, "usage: switchframe-asr --shm-name NAME --socket PATH\n")
		os.Exit(1)
	}

	// Open shm reader.
	reader, err := shmring.NewReader(*shmName)
	if err != nil {
		slog.Error("failed to open shm", "error", err)
		os.Exit(1)
	}
	defer func() { _ = reader.Close() }()

	// Connect to Unix socket.
	conn, err := net.Dial("unix", *socketPath)
	if err != nil {
		slog.Error("failed to connect to socket", "error", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	// Create audio processor (48kHz stereo -> 16kHz mono).
	audioProc := newAudioProcessor(48000, 2)

	// Backend registry: track which backends are available and which is active.
	// Only the default backend is loaded eagerly; others load on demand to
	// conserve GPU VRAM (~1.5-2GB per model).
	availableBackendNames := []string{}
	var activeBackend asrBackend

	// Track configs for lazy loading.
	trtModelDir := *modelDir
	whisperDir := *whisperModelsDir
	deviceID := *device

	if trtModelDir != "" {
		availableBackendNames = append(availableBackendNames, "parakeet")
	}
	if whisperDir != "" {
		availableBackendNames = append(availableBackendNames, "whisper")
	}

	// loadBackend initializes a backend by name, closing the previous one.
	loadBackend := func(name string) (asrBackend, error) {
		switch name {
		case "parakeet":
			if trtModelDir == "" {
				return nil, fmt.Errorf("parakeet: --model-dir not set")
			}
			return newTRTASRBackend(trtModelDir, deviceID)
		case "whisper":
			if whisperDir == "" {
				return nil, fmt.Errorf("whisper: --whisper-models-dir not set")
			}
			threads := resolveWhisperThreads(*whisperThreads, runtime.NumCPU())
			return newWhisperASRBackend(whisperDir, true, threads)
		default:
			return nil, fmt.Errorf("unknown backend %q", name)
		}
	}

	// Load only the default backend.
	defaultName := *defaultBackend
	if defaultName == "" && len(availableBackendNames) > 0 {
		defaultName = availableBackendNames[0]
	}
	if defaultName != "" {
		b, err := loadBackend(defaultName)
		if err != nil {
			slog.Warn("default backend unavailable, trying others", "backend", defaultName, "error", err)
			// Try the other available backend.
			for _, name := range availableBackendNames {
				if name != defaultName {
					b, err = loadBackend(name)
					if err == nil {
						break
					}
				}
			}
		}
		if b != nil {
			activeBackend = b
			defer activeBackend.Close()
			slog.Info("active backend selected", "backend", activeBackend.Name(), "model", activeBackend.ModelName())
		} else {
			slog.Warn("no ASR backends available, running as pass-through")
		}
	}

	// Initialize Silero VAD (ONNX Runtime, CPU — no GPU stream needed).
	var sVAD *sileroVAD
	vadPath := *vadModelPath
	if vadPath == "" && trtModelDir != "" {
		candidate := filepath.Join(trtModelDir, "silero_vad.onnx")
		if _, err := os.Stat(candidate); err == nil {
			vadPath = candidate
		}
	}
	if vadPath != "" {
		var gpuStream unsafe.Pointer
		if tb, ok := activeBackend.(*trtASRBackend); ok {
			gpuStream = tb.engine.backend.gpuStream()
		}
		var err error
		sVAD, err = newSileroVAD(vadPath, gpuStream, 0.5, 750)
		if err != nil {
			slog.Warn("silero VAD unavailable, using energy-only", "error", err)
		} else {
			defer sVAD.close()
			slog.Info("silero VAD initialized", "model", vadPath)
		}
	}

	// Initialize energy VAD.
	eVAD := newEnergyVAD(-26, 500, 16000)

	// Signal READY to parent process.
	fmt.Println("READY")

	// Send initial status over IPC socket with backend info.
	sendStatus(conn, activeBackend, availableBackendNames, eVAD)

	// Setup signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Control message reader goroutine.
	controlCh := make(chan *asripc.Message, 8)
	go func() {
		for {
			msg, err := asripc.ReadMessage(conn)
			if err != nil {
				return // socket closed
			}
			controlCh <- msg
		}
	}()

	// Main loop: 80ms ticker reads audio from shm, processes it through
	// dual VAD (energy + Silero), and triggers inference on speech-end.
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	running := true
	var speechBuf []float32
	var wasSpeaking bool
	var speechStart time.Time
	maxAccum := 5 * time.Second

	for {
		select {
		case <-ticker.C:
			if !running {
				continue
			}
			samples := reader.Read()
			if len(samples) == 0 {
				continue
			}
			mono16k := audioProc.process(samples)
			if len(mono16k) == 0 {
				continue
			}

			// Quick energy gate.
			if computeRMS(mono16k) < *energyThreshold {
				continue
			}

			// Dual VAD: both must agree.
			eVAD.process(mono16k)
			if sVAD != nil {
				sVAD.process(mono16k)
			}
			energySpeaking := eVAD.isSpeaking()
			sileroSpeaking := sVAD == nil || sVAD.isSpeaking()
			nowSpeaking := energySpeaking && sileroSpeaking

			switch {
			case nowSpeaking && !wasSpeaking:
				// Speech started.
				speechBuf = speechBuf[:0]
				speechStart = time.Now()
				wasSpeaking = true
				speechBuf = append(speechBuf, mono16k...)

			case nowSpeaking:
				// Speech continues.
				speechBuf = append(speechBuf, mono16k...)
				// Max accumulation: forced transcription.
				if time.Since(speechStart) >= maxAccum && activeBackend != nil {
					words := activeBackend.Transcribe(speechBuf)
					emitWords(conn, words)
					speechBuf = speechBuf[:0]
					speechStart = time.Now()
				}

			case !nowSpeaking && wasSpeaking:
				// Speech ended.
				speechBuf = append(speechBuf, mono16k...)
				wasSpeaking = false
				if activeBackend != nil {
					words := activeBackend.Transcribe(speechBuf)
					emitWords(conn, words)
				}
				speechBuf = speechBuf[:0]
			}

		case msg := <-controlCh:
			switch msg.Cmd {
			case "shutdown":
				slog.Info("shutdown requested")
				return
			case "stop":
				running = false
			case "start":
				running = true
			case "reset":
				audioProc = newAudioProcessor(48000, 2)
				eVAD.reset()
				if sVAD != nil {
					sVAD.reset()
				}
				speechBuf = speechBuf[:0]
				wasSpeaking = false
			case "set_config":
				var cfg map[string]interface{}
				if err := json.Unmarshal(msg.Config, &cfg); err != nil {
					slog.Warn("invalid set_config", "error", err)
					break
				}
				if backend, ok := cfg["backend"].(string); ok && backend != "" {
					if activeBackend == nil || backend != activeBackend.Name() {
						newBackend, err := loadBackend(backend)
						if err != nil {
							slog.Warn("failed to load backend", "backend", backend, "error", err)
						} else {
							// Close old backend to free GPU VRAM.
							if activeBackend != nil {
								activeBackend.Close()
							}
							activeBackend = newBackend
							slog.Info("switched backend", "backend", backend)
						}
					}
				}
				if model, ok := cfg["model"].(string); ok && model != "" && activeBackend != nil {
					if err := activeBackend.SetModel(model); err != nil {
						slog.Warn("set model failed", "error", err)
					}
				}
				if nf, ok := cfg["noiseFloor"].(string); ok {
					if db, valid := noiseFloorToDB(nf); valid {
						eVAD.setThresholdDB(db)
					}
				}
				if activeBackend != nil {
					activeBackend.SetConfig(cfg)
				}
				sendStatus(conn, activeBackend, availableBackendNames, eVAD)
			}

		case <-sigCh:
			slog.Info("signal received, shutting down")
			return
		}
	}
}
