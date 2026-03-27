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
	"syscall"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/asr/asripc"
	"github.com/zsiec/switchframe/server/asr/shmring"
)

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
func sendStatus(conn net.Conn, active asrBackend, backends []asrBackend) {
	names := make([]string, len(backends))
	for i, b := range backends {
		names[i] = b.Name()
	}
	msg := &asripc.Message{
		Type:              "status",
		State:             "ready",
		AvailableBackends: names,
	}
	if active != nil {
		msg.Backend = active.Name()
		msg.Model = active.ModelName()
		msg.Models = active.Models()
	}
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
	defer reader.Close()

	// Connect to Unix socket.
	conn, err := net.Dial("unix", *socketPath)
	if err != nil {
		slog.Error("failed to connect to socket", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Create audio processor (48kHz stereo -> 16kHz mono).
	audioProc := newAudioProcessor(48000, 2)

	// Initialize backend registry.
	var backends []asrBackend
	var activeBackend asrBackend

	// Init TRT backend (if model-dir provided).
	if *modelDir != "" {
		trt, err := newTRTASRBackend(*modelDir, *device)
		if err != nil {
			slog.Warn("parakeet backend unavailable", "error", err)
		} else {
			backends = append(backends, trt)
			defer trt.Close()
		}
	}

	// Init Whisper backend (if whisper-models-dir provided).
	if *whisperModelsDir != "" {
		whisper, err := newWhisperASRBackend(*whisperModelsDir, true)
		if err != nil {
			slog.Warn("whisper backend unavailable", "error", err)
		} else {
			backends = append(backends, whisper)
			defer whisper.Close()
		}
	}

	// Select default backend.
	for _, b := range backends {
		if b.Name() == *defaultBackend {
			activeBackend = b
			break
		}
	}
	if activeBackend == nil && len(backends) > 0 {
		activeBackend = backends[0]
	}

	if activeBackend != nil {
		slog.Info("active backend selected", "backend", activeBackend.Name(), "model", activeBackend.ModelName())
	} else {
		slog.Warn("no ASR backends available, running as pass-through")
	}

	// Find GPU stream for Silero VAD from TRT backend.
	var gpuStream unsafe.Pointer
	for _, b := range backends {
		if tb, ok := b.(*trtASRBackend); ok {
			gpuStream = tb.engine.backend.gpuStream()
			break
		}
	}

	// Initialize Silero VAD if GPU stream is available.
	var sVAD *sileroVAD
	if gpuStream != nil {
		vadPath := *vadModelPath
		if vadPath == "" && *modelDir != "" {
			candidate := filepath.Join(*modelDir, "silero_vad.onnx")
			if _, err := os.Stat(candidate); err == nil {
				vadPath = candidate
			}
		}
		if vadPath != "" {
			var err error
			sVAD, err = newSileroVAD(vadPath, gpuStream, 0.5, 750)
			if err != nil {
				slog.Warn("silero VAD unavailable, using energy-only", "error", err)
			} else {
				defer sVAD.close()
				slog.Info("silero VAD initialized", "model", vadPath)
			}
		}
	}

	// Initialize energy VAD.
	eVAD := newEnergyVAD(-26, 500, 16000)

	// Signal READY to parent process.
	fmt.Println("READY")

	// Send initial status over IPC socket with backend info.
	sendStatus(conn, activeBackend, backends)

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
				var cfg struct {
					Backend string `json:"backend"`
					Model   string `json:"model"`
				}
				if err := json.Unmarshal(msg.Config, &cfg); err != nil {
					slog.Warn("invalid set_config", "error", err)
					break
				}
				if cfg.Backend != "" && (activeBackend == nil || cfg.Backend != activeBackend.Name()) {
					for _, b := range backends {
						if b.Name() == cfg.Backend {
							activeBackend = b
							slog.Info("switched backend", "backend", cfg.Backend)
							break
						}
					}
				}
				if cfg.Model != "" && activeBackend != nil {
					if err := activeBackend.SetModel(cfg.Model); err != nil {
						slog.Warn("set model failed", "error", err)
					}
				}
				sendStatus(conn, activeBackend, backends)
			}

		case <-sigCh:
			slog.Info("signal received, shutting down")
			return
		}
	}
}
