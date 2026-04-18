# ASR (Automatic Speech Recognition) / AI Captioning

Real-time speech-to-text from the live program audio, fed into the CEA-608 caption system for on-screen display and MPEG-TS embedding. Uses [whisper.cpp](https://github.com/ggerganov/whisper.cpp) for inference and Silero VAD for neural speech detection.

**Contents:** [Architecture](#1-architecture) | [Setup & Configuration](#2-setup--configuration) | [Inference Engine](#3-inference-engine) | [Caption Pacer](#4-caption-pacer) | [Tuning Presets](#5-tuning-presets) | [REST API](#6-rest-api) | [Voice Activity Detection](#7-voice-activity-detection) | [State Broadcast](#8-state-broadcast) | [Performance](#9-performance) | [Limitations](#10-limitations)

---

## 1. Architecture

```
Mixer output (48kHz stereo float32)
  |
  v
ASR Engine (server/asr/engine.go)
  |
  +-> Resampler (48kHz stereo -> 16kHz mono, linear interpolation)
  +-> Ring buffer (30s circular, 480,000 samples = ~1.92 MB)
  +-> VAD gate (Silero neural or energy-based RMS fallback)
  +-> Whisper.cpp inference (CUDA GPU or CPU, beam search)
  +-> Transcript callback ([]TranscriptWord with confidence + gap timing)
  |
  v
Caption Pacer (server/cmd/switchframe/app_asr.go)
  |
  +-> Adaptive word drain (1-3 words/tick at 6 ticks/sec base rate)
  +-> Confidence holdback (low-confidence words held one tick)
  +-> Speaker change detection (segment gap > threshold -> >> prefix)
  +-> CEA-608 line wrapping (32 char max)
  +-> Auto-clear after 5s silence
  |
  v
Caption Manager (server/caption/manager.go)
  |
  +-> CEA-608 encoding
  +-> SEI injection (H.264 output)
  +-> VANC output (MXL)
```

Audio enters from the mixer's raw audio sink. The ASR engine resamples to 16kHz mono (Whisper's native format), buffers in a 30-second circular ring, and gates inference with VAD. When speech is detected, whisper.cpp transcribes the buffered audio and returns per-word text with confidence scores and inter-segment gap timing. The caption pacer then drains words at a regulated rate into the existing CEA-608 caption pipeline.

### Key source files

| File | Purpose |
|------|---------|
| `server/asr/engine.go` | Engine lifecycle, VAD-triggered inference, adaptive timing |
| `server/asr/whisper_cpp.go` | Whisper.cpp cgo bindings, Silero VAD, beam search, confidence |
| `server/asr/whisper_cpp_stub.go` | Stub for non-whisper builds (returns `ErrASRNotAvailable`) |
| `server/asr/vad.go` | Energy-based voice activity detection state machine |
| `server/asr/resample.go` | 48kHz stereo to 16kHz mono resampler (linear interpolation) |
| `server/asr/ringbuf.go` | Circular audio buffer (30s at 16kHz) |
| `server/asr/types.go` | `TranscriptWord` type (text, confidence, gap) |
| `server/cmd/switchframe/app_asr.go` | App wiring, caption pacer, audio sink fan-out |
| `server/control/api_asr.go` | REST API endpoints |

---

## 2. Setup & Configuration

### Prerequisites

- **Build tags:** `cgo && whisper` (the stub returns `ErrASRNotAvailable` otherwise). Linker flags split per platform: `backend_whisper_cuda.go` (Linux) and `backend_whisper_metal.go` (Darwin).
- **Captions enabled:** `--captions` flag is required (ASR feeds into the caption manager)
- **whisper.cpp:** Compiled and installed at `/opt/whisper.cpp` (the cgo flags reference this path)
- **GPU acceleration:** Linux builds link CUDA (`-lcublas -lcudart`); macOS builds link Metal + Accelerate frameworks. CPU-only fallback is automatic at runtime if GPU init fails.

### Building whisper.cpp

**Linux (CUDA):**

```bash
sudo git clone https://github.com/ggerganov/whisper.cpp /opt/whisper.cpp
cd /opt/whisper.cpp
sudo cmake -B build -DGGML_CUDA=ON
sudo cmake --build build -j --config Release
```

**macOS (Metal):**

```bash
sudo git clone https://github.com/ggerganov/whisper.cpp /opt/whisper.cpp
cd /opt/whisper.cpp
sudo cmake -B build -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON
sudo cmake --build build -j --config Release
```

`GGML_METAL_EMBED_LIBRARY=ON` bakes the Metal shader library into `libggml-metal.a`, so the runtime does not need to find a `default.metallib` next to the binary.

Then build the sidecar with the right tags for your platform:

```bash
make build-asr-sidecar-whisper   # auto-selects CUDA on Linux, Metal on macOS
```

### Model download

Download GGML-format Whisper models from the [whisper.cpp models repository](https://huggingface.co/ggerganov/whisper.cpp/tree/main). Place them in a directory together:

```
/path/to/models/
  ggml-base.bin            # ~148 MB, fastest
  ggml-base.en.bin         # English-only variant
  ggml-medium.en.bin       # ~1.5 GB, good accuracy
  ggml-large-v3.bin        # ~3.1 GB, best accuracy
  ggml-silero-v6.2.0.bin   # Silero VAD model (auto-detected)
```

### Silero VAD auto-detection

When `--asr-model` is set, the app automatically looks for a Silero VAD model in the same directory as the Whisper model. It checks for these filenames in order:

1. `ggml-silero-v6.2.0.bin`
2. `ggml-silero-v5.1.2.bin`

If found, Silero VAD is enabled inside whisper.cpp, which filters non-speech audio (music, background noise, applause, silence) before transcription. Without it, the system falls back to the energy-based RMS VAD.

### CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--asr-model` | (empty) | Path to a Whisper GGML model file. Empty = ASR disabled. |
| `--captions` | (off) | Required for ASR output. Enables the caption manager. |

### Example launch

```bash
./switchframe --captions --asr-model /opt/whisper.cpp/models/ggml-medium.en.bin
```

ASR initialization is **non-fatal**: if the engine fails to load (missing model, missing whisper.cpp library), the server starts normally with ASR unavailable. The log will show a warning.

---

## 3. Inference Engine

The engine (`server/asr/engine.go`) orchestrates the audio-to-text pipeline with three key design decisions: adaptive timing for fast feedback, VAD gating to avoid wasted compute, and growing-window deduplication for accuracy.

### Adaptive timing

Inference does not run at a fixed interval. The first inference fires quickly (1 second after speech starts) to get words on screen fast, then ramps up:

| Inference # | Interval | Rationale |
|-------------|----------|-----------|
| 1st | 1000 ms | Fast initial words on screen |
| 2nd | 2000 ms | Balance speed and accuracy |
| 3rd+ | `inferMs` (default 3000 ms) | Steady state, configurable via speed preset |

### VAD-gated inference

Inference only runs when the VAD detects speech:

- **Idle -> Speaking:** Reset state, start timing for first inference.
- **Speaking / Trailing:** Run inference at the adaptive interval.
- **Trailing -> Idle:** Run one final inference with `isFinal=true`, clear the ring buffer.

No CPU/GPU cycles are spent on silence, music, or background noise.

### Growing-window deduplication

Non-final inferences do **not** drain the ring buffer. Whisper re-transcribes the entire growing speech segment each time, which gives it full context for better accuracy. Only **new** words beyond `lastWordCount` are emitted to the callback. This avoids duplicates while letting Whisper refine earlier words internally.

On final inference (speech ended), the buffer is cleared and any remaining new words are emitted.

### Energy pre-check

Before calling whisper.cpp, `runInference` checks the RMS energy of the buffered audio. If `rms < 0.02`, inference is skipped entirely. This is a cheap guard against running the model on near-silence that passed the VAD threshold.

### Minimum audio check

Inference requires at least 1600 samples (~100ms at 16kHz) to proceed. Shorter buffers are skipped.

### Hallucination suppression

Whisper.cpp is configured with multiple anti-hallucination parameters:

| Parameter | Value | Effect |
|-----------|-------|--------|
| `suppress_blank` | `true` | Suppress blank outputs |
| `suppress_nst` | `true` | Suppress non-speech tokens |
| `no_speech_thold` | 0.5 | Skip segments where P(no_speech) > 50% |
| `logprob_thold` | -1.5 | Skip tokens with low average log probability |
| `entropy_thold` | 2.4 | Skip segments with high entropy (random/hallucinated text) |
| `beam_search.beam_size` | 5 | Beam search for quality over greedy decoding |

Special tokens are filtered by both token ID checks and text pattern matching (`[` or `<` prefixes like `[_EOT_]`, `<|endoftext|>`).

### Language support

- **Auto-detect:** Default language is `"auto"` -- Whisper detects the spoken language.
- **Explicit:** Set via `engine.SetLanguage("fr")` or the REST API. Any ISO 639-1 code supported by Whisper (100+ languages).
- **Translation:** Enabled by default (`translate: true`). Whisper transcribes in the detected language, then translates to English. Disable via REST API for native-language captions.

### Runtime model swap

`engine.SetModel(path)` swaps the Whisper model at runtime. It serializes with the `inferring` mutex to ensure no concurrent inference, loads the new model, frees the old context, and clears the audio buffer and VAD state.

### Concurrency

- `engine.mu` protects ring buffer, VAD, callback, and inference state.
- `engine.inferring` (a separate mutex) serializes whisper.cpp inference calls -- only one inference runs at a time.
- `runInference` is launched as a goroutine from `IngestAudio` to avoid blocking the audio path.

---

## 4. Caption Pacer

The caption pacer (`captionPacer` in `server/cmd/switchframe/app_asr.go`) sits between the ASR engine and the caption manager, regulating word output for readable on-screen captions.

### Drain rate

The pacer runs a ticker at 6 ticks/second (the maximum drain rate). The number of words emitted per tick scales with queue depth:

| Queue depth | Words/tick | Effective rate | Behavior |
|-------------|-----------|---------------|----------|
| 0-5 | 1 | ~6 WPS (~180 WPM) | Comfortable reading pace |
| 6-15 | 2 | ~12 WPS | Catching up |
| 16+ | 3 | ~18 WPS | Fast drain to recover |

This adaptive rate keeps captions in sync with speech regardless of speaking speed, while avoiding overwhelming the viewer during fast segments.

### Confidence holdback

Words with confidence below the threshold (default 0.7) are held for one tick rather than emitted immediately. This gives the next inference cycle a chance to correct them. After one tick, held words are emitted regardless -- in live captioning, late is better than missing.

### Speaker change detection

Whisper creates new segments when it detects pauses. The engine measures inter-segment timestamp gaps and attaches `GapBefore` (in milliseconds) to the first word of each new segment. When a word's `GapBefore` exceeds the speaker change threshold (default 300ms), the pacer:

1. Inserts a newline
2. Emits a `>> ` prefix
3. Resets the line position to 3

### CEA-608 line wrapping

CEA-608 captions are limited to 32 characters per line. The pacer tracks the current line position and wraps to a new line when the next word (plus a space) would exceed the limit.

### Auto-clear

If no new words are emitted for 5 seconds, the pacer calls `mgr.Clear()` to erase the displayed caption. This prevents stale text from lingering on screen during extended silence.

### Queue management

The queue is capped at 50 words (`pacerMaxQueueWords`). When exceeded, the oldest words are dropped. This prevents unbounded memory growth if inference produces words faster than the pacer can drain them.

### Final words

When an inference result is marked `isFinal` (speech ended), the last word in the batch gets `isFinal = true`. When emitted, this triggers a newline to close the utterance.

### Program change

When the program source changes, `resetASROnProgramChange()` is called, which:

1. Resets the ASR engine (clears ring buffer and VAD state)
2. Flushes the pacer queue (discards all pending words, clears held word)
3. Clears the caption display

This prevents stale text from the old source from draining through the pacer.

---

## 5. Tuning Presets

Four tuning dimensions are available, each with three named presets. All are changeable at runtime via the REST API.

### Speed (inference interval)

Controls the steady-state interval between inference runs. The first two inferences always use the adaptive 1s/2s ramp regardless of this setting.

| Preset | Interval | Description |
|--------|----------|-------------|
| `fast` | 1000 ms | Lowest latency, may be less accurate |
| `balanced` | 3000 ms | Default. Good balance of speed and accuracy |
| `accurate` | 5000 ms | Highest accuracy, more latency |

### Confidence filter (holdback threshold)

Controls which words are held back one tick for potential correction.

| Preset | Threshold | Description |
|--------|-----------|-------------|
| `low` | 0.50 | Most words pass through immediately |
| `medium` | 0.70 | Default. Moderate holdback |
| `high` | 0.90 | Only very confident words pass immediately |

### Noise floor (VAD sensitivity)

Controls the energy-based VAD RMS threshold. Lower values detect quieter speech but are more sensitive to background noise. This setting affects the backup energy VAD; when Silero VAD is enabled in whisper.cpp, it provides the primary speech filtering.

| Preset | Threshold | Description |
|--------|-----------|-------------|
| `quiet` | -30 dBFS | Detects quiet speech, sensitive to noise |
| `normal` | -26 dBFS | Default. Above typical background music/noise |
| `noisy` | -20 dBFS | Only detects louder speech, ignores more noise |

### Speaker detection (gap threshold)

Controls the Whisper inter-segment gap required to trigger a speaker change (`>>` prefix on a new line).

| Preset | Gap | Description |
|--------|-----|-------------|
| `sensitive` | 200 ms | Triggers on short pauses |
| `normal` | 300 ms | Default. Natural conversation pauses |
| `relaxed` | 500 ms | Only triggers on longer pauses |

---

## 6. REST API

### `GET /api/asr/status`

Returns the current ASR system state.

**Response:**
```json
{
  "available": true,
  "active": true,
  "translate": true,
  "modelName": "ggml-medium.en.bin",
  "language": "auto",
  "speed": "balanced",
  "confidenceFilter": "medium",
  "noiseFloor": "normal",
  "speakerDetection": "normal"
}
```

- `available`: Whether the whisper.cpp backend is loaded.
- `active`: Whether ASR is currently feeding captions (caption mode is `auto`).

Returns `{"available": false}` when ASR is not initialized.

### `PUT /api/asr/config`

Updates ASR configuration. All fields are optional -- include only the fields you want to change. Returns 501 if ASR is not available. Triggers a state broadcast after applying changes.

**Request body:**
```json
{
  "active": true,
  "language": "fr",
  "translate": false,
  "model": "large-v3",
  "speed": "accurate",
  "confidenceFilter": "high",
  "noiseFloor": "quiet",
  "speakerDetection": "sensitive"
}
```

| Field | Type | Values |
|-------|------|--------|
| `active` | bool | Enable/disable ASR captioning |
| `language` | string | ISO 639-1 code or `"auto"` |
| `translate` | bool | Translate to English |
| `model` | string | Model name (e.g., `"base"`, `"medium.en"`, `"large-v3"`) |
| `speed` | string | `"fast"`, `"balanced"`, `"accurate"` |
| `confidenceFilter` | string | `"low"`, `"medium"`, `"high"` |
| `noiseFloor` | string | `"quiet"`, `"normal"`, `"noisy"` |
| `speakerDetection` | string | `"sensitive"`, `"normal"`, `"relaxed"` |

**Response:** Updated `ASRState` object (same format as `GET /api/asr/status`).

**Model swap:** Setting `model` triggers a runtime model swap. The engine blocks until any in-flight inference completes, loads the new model from the models directory (`ggml-{name}.bin`), frees the old model, and clears the audio buffer. Returns 400 if the model file cannot be loaded.

### `GET /api/asr/models`

Lists available Whisper models found in the models directory (the directory containing the model specified by `--asr-model`).

**Response:**
```json
["base", "base.en", "medium.en", "large-v3"]
```

Scans for files matching `ggml-*.bin`, excluding test files (`for-tests-*`) and Silero VAD models (`*silero*`). Returns 501 if ASR is not available.

---

## 7. Voice Activity Detection

The VAD (`server/asr/vad.go`) is an energy-based speech detector using RMS amplitude. It acts as the gating mechanism for inference, ensuring whisper.cpp only runs when speech is present.

**Note:** When a Silero VAD model is available, whisper.cpp also runs its own neural VAD internally, which is significantly more accurate than energy-based detection. The energy VAD in `vad.go` still controls when inference is triggered, while Silero VAD inside whisper.cpp filters the actual audio segments before transcription.

### State machine

```
        speech detected
  Idle -----------------> Speaking
   ^                         |
   |                         | silence detected
   |                         v
   +---- hangover ---- Trailing
         expired
```

Three states:

| State | Meaning |
|-------|---------|
| `Idle` | No speech detected. Inference is not triggered. |
| `Speaking` | Active speech. Inference runs at the adaptive interval. |
| `Trailing` | Speech ended but within the hangover period. Inference still eligible. |

### Speech detection

Each audio chunk's RMS energy is computed and compared against the linear threshold (derived from the dBFS setting via `10^(dB/20)`). If RMS >= threshold, the chunk is classified as speech.

### Hangover timer

The hangover prevents premature cutoff during natural speech pauses (breaths, brief hesitations). Default: 500ms (8000 samples at 16kHz). During the `Trailing` state, silence samples accumulate. If speech resumes before the hangover expires, the state returns to `Speaking`. If silence persists past the hangover, the state transitions to `Idle` and a final inference is triggered.

### Configuration

The threshold can be changed at runtime via `SetThresholdDB()`, which recalculates the linear threshold. The `Reset()` method returns the VAD to `Idle` -- used on program source changes.

---

## 8. State Broadcast

ASR state is included in the `ControlRoomState` broadcast as the `asr` field (`internal.ASRState`):

```go
type ASRState struct {
    Available        bool    `json:"available"`
    Active           bool    `json:"active"`
    Translate        bool    `json:"translate"`
    ModelName        string  `json:"modelName,omitempty"`
    Language         string  `json:"language,omitempty"`
    Tentative        string  `json:"tentative,omitempty"`
    Confidence       float64 `json:"confidence,omitempty"`
    Speed            string  `json:"speed,omitempty"`
    ConfidenceFilter string  `json:"confidenceFilter,omitempty"`
    NoiseFloor       string  `json:"noiseFloor,omitempty"`
    SpeakerDetection string  `json:"speakerDetection,omitempty"`
}
```

State enrichment happens in `enrichASRState()`, which reads the current engine settings (language, translate, speed, VAD sensitivity) and pacer settings (confidence filter, speaker detection) to populate the broadcast.

---

## 9. Performance

### Inference speed

Inference time depends on model size, audio duration, and hardware:

| Model | Size | GPU (CUDA) | CPU |
|-------|------|------------|-----|
| base | ~148 MB | ~100-200 ms for 3s audio | ~500-800 ms |
| medium.en | ~1.5 GB | ~300-500 ms for 3s audio | ~2-5 s |
| large-v3 | ~3.1 GB | ~500-800 ms for 3s audio | ~5-15 s |

GPU acceleration is enabled by default (`UseGPU: true`). On a system with a capable NVIDIA GPU and CUDA installed, inference is significantly faster, especially for medium and large models.

### Memory usage

| Component | Memory |
|-----------|--------|
| Ring buffer | ~1.92 MB (480,000 float32 samples) |
| base model | ~148 MB |
| medium.en model | ~1.5 GB |
| large-v3 model | ~3.1 GB |
| Silero VAD model | ~2 MB |

### Threading

- Whisper.cpp inference uses 4 threads (`params.n_threads = 4`).
- The `inferring` mutex ensures only one inference runs at a time.
- Inference runs in a goroutine to avoid blocking the audio ingest path.
- The caption pacer runs its own goroutine with a 167ms ticker (6 Hz).

---

## 10. Limitations

- **Latency:** ~2-5 seconds from speech to caption display. This is the sum of: ring buffer accumulation (1-3s adaptive timing), whisper.cpp inference (0.1-0.8s on GPU), and pacer drain delay (~0.2-0.5s). The adaptive ramp helps -- the first words appear ~1.5s after speech starts.

- **Accuracy:** Heavily dependent on model size and audio quality. The `base` model is fast but less accurate; `large-v3` is highly accurate but requires significant compute. English-only models (`.en` suffix) are more accurate for English than multilingual models of the same size.

- **GPU recommended:** CPU inference is viable for the `base` model but impractical for `medium` or `large` models in real-time scenarios. A CUDA-capable GPU is strongly recommended for production use.

- **Build dependency:** Requires whisper.cpp compiled and installed at `/opt/whisper.cpp`. The cgo CFLAGS and LDFLAGS in `whisper_cpp.go` hardcode this path. CUDA libraries must be available at `/usr/local/cuda/lib64`.

- **Single-language per session:** While language can be changed at runtime, Whisper processes each inference call in one language. It cannot handle mixed-language speech within a single segment.

- **Translation direction:** Translation is to English only (a Whisper limitation). Non-English to non-English translation is not supported.

- **Sensitivity stored but not yet applied:** The `Sensitivity` field in `ASRState` is stored but not currently used as an applied threshold -- the `NoiseFloor` preset controls the actual VAD behavior.

- **Resampler quality:** The 48kHz to 16kHz resampler uses linear interpolation, which is sufficient for speech recognition but would not be acceptable for high-fidelity audio processing. This is intentional -- Whisper does not benefit from higher-quality resampling.
