# MXL Integration Guide

SwitchFrame supports [MXL](https://tech.ebu.ch/dmf/mxl) (Media eXchange Layer) for shared-memory media I/O. MXL is the EBU/Linux Foundation open-source SDK for zero-copy, real-time media exchange between software processes on the same host.

This enables SwitchFrame to interoperate with MXL-compatible broadcast tools — receiving uncompressed V210 video and float32 audio as sources, and publishing the program output back to MXL shared memory for downstream consumers.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Building with MXL](#building-with-mxl)
- [Running the MXL Demo](#running-the-mxl-demo)
- [Configuration](#configuration)
- [Flow Discovery](#flow-discovery)
- [Source Configuration](#source-configuration)
- [Program Output](#program-output)
- [How It Works](#how-it-works)
- [Flow Definition Files](#flow-definition-files)
- [Troubleshooting](#troubleshooting)
- [Testing](#testing)

---

## Overview

### What MXL Provides

| Feature | Description |
|---------|-------------|
| **V210 video** | Uncompressed 10-bit YCbCr 4:2:2 in shared-memory ring buffers |
| **Float32 audio** | De-interleaved floating-point PCM in continuous ring buffers |
| **NMOS IS-04 discovery** | Flow definitions as JSON files in the domain directory |
| **Zero-copy transport** | POSIX shared memory, no network or codec overhead |

### Why Use MXL?

- **No compression artifacts**: Raw uncompressed video avoids encode/decode cycles between tools
- **Sub-millisecond latency**: Shared memory is faster than any network transport
- **Interoperability**: Connect SwitchFrame to other MXL-compatible tools (GStreamer, FFmpeg plugins, hardware I/O cards)
- **Professional formats**: V210 10-bit 4:2:2 is the standard uncompressed format in broadcast

### Architecture

MXL sources bypass the normal MoQ/WebTransport ingest path. Instead of receiving H.264 streams and decoding them, MXL sources provide raw YUV data directly to the switching pipeline:

```
Standard path:  Camera → MoQ H.264 → Prism Relay → H.264 Decode → YUV420p → Switcher
MXL path:       MXL App → Shared Mem → V210→YUV420p conversion → Switcher (no codec)
```

MXL sources also get encoded to H.264/AAC for browser multiview monitoring — operators see MXL sources in the browser UI just like any other source.

---

## Prerequisites

### MXL SDK

Build and install the MXL SDK (v1.0.0+):

- C++ with C API, Apache 2.0 license
- Produces `libmxl` shared library + headers + pkg-config file
- Includes `mxl-gst-testsrc` and `mxl-gst-sink` GStreamer tools

After building, set `MXL_ROOT` to your install prefix:

```bash
# macOS
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release

# Linux
export MXL_ROOT=$HOME/dev/mxl/install/Linux-GCC-Release
```

### Shared Memory Domain

MXL requires a shared-memory directory:

**macOS** (RAM disk):
```bash
diskutil erasevolume HFS+ MXL $(hdiutil attach -nomount ram://2097152)
# Creates /Volumes/MXL (1 GB RAM disk)
```

**Linux** (tmpfs — usually already available):
```bash
mkdir -p /dev/shm/mxl
```

### Additional Dependencies

- **GStreamer** — required for `mxl-gst-testsrc` test source generator (demo only)
- **Go 1.25+**, **Node.js 22+** — standard SwitchFrame build requirements
- **FFmpeg + FDK-AAC** — standard SwitchFrame codec dependencies

---

## Building with MXL

### Makefile Target

```bash
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release
make build-server-mxl
# Output: bin/switchframe (with MXL support)
```

### Manual Build

```bash
cd server
PKG_CONFIG_PATH="${MXL_ROOT}/lib/pkgconfig" \
  go build -tags "cgo mxl" -o ../bin/switchframe ./cmd/switchframe
```

The `mxl` build tag activates the real cgo bindings in `server/mxl/flow.go`. Without it, the stub implementation returns `ErrMXLNotAvailable` for all MXL operations.

### Production Build with Embedded UI

```bash
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release
cd ui && npm ci && npm run build && cd ..
ln -sf ../../ui/build server/cmd/switchframe/ui
cd server
PKG_CONFIG_PATH="${MXL_ROOT}/lib/pkgconfig" \
  go build -tags "cgo mxl embed_ui" -o ../bin/switchframe ./cmd/switchframe
```

---

## Running the MXL Demo

The `make mxl-demo` target runs a full end-to-end demo:

```bash
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release
make mxl-demo
```

This:

1. **Starts 2 GStreamer test sources** writing to MXL shared memory:
   - Source 1: SMPTE color bars (1920x1080 29.97fps) + stereo audio
   - Source 2: Checkerboard pattern (1920x1080 29.97fps) + stereo audio

2. **Builds SwitchFrame** with `-tags "cgo mxl"`

3. **Launches SwitchFrame** with `--demo` (adds 4 synthetic H.264 cameras alongside the 2 MXL sources) and `--mxl-output program` (routes program back to MXL)

4. **Starts the UI dev server** at http://localhost:5173

The demo shows all 6 sources (4 synthetic + 2 MXL) in the browser multiview. Cut between them, apply transitions, and see the program output in real time.

### Monitoring MXL Program Output

View the program output with `mxl-gst-sink`:

```bash
export DYLD_LIBRARY_PATH=$MXL_ROOT/lib  # macOS
# export LD_LIBRARY_PATH=$MXL_ROOT/lib  # Linux

$MXL_ROOT/bin/mxl-gst-sink \
  -d /Volumes/MXL \
  -v b0000001-0000-0000-0000-000000000001 \
  -a b0000001-0000-0000-0000-000000000002
```

### Demo UUIDs

The demo uses flow definitions from `test/mxl/*.json`:

| Flow | UUID | File |
|------|------|------|
| Source 1 video | `a0000001-0000-0000-0000-000000000001` | `src1_video.json` |
| Source 1 audio | `a0000001-0000-0000-0000-000000000002` | `src1_audio.json` |
| Source 2 video | `a0000002-0000-0000-0000-000000000001` | `src2_video.json` |
| Source 2 audio | `a0000002-0000-0000-0000-000000000002` | `src2_audio.json` |
| Output video | `b0000001-0000-0000-0000-000000000001` | `output_video.json` |
| Output audio | `b0000001-0000-0000-0000-000000000002` | `output_audio.json` |

---

## Configuration

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mxl-sources` | `""` | Comma-separated source specs (see [Source Configuration](#source-configuration)) |
| `--mxl-output` | `""` | MXL flow name for program output (empty = disabled) |
| `--mxl-output-video-def` | `""` | Path to NMOS IS-04 video flow definition JSON for output |
| `--mxl-output-audio-def` | `""` | Path to NMOS IS-04 audio flow definition JSON for output |
| `--mxl-domain` | `/dev/shm/mxl` | MXL shared memory domain directory |
| `--mxl-discover` | `false` | List available flows and exit |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SWITCHFRAME_MXL_SOURCES` | Source specs (same format as `--mxl-sources`). CLI flag takes precedence. |
| `MXL_ROOT` | MXL SDK install directory (build-time only) |
| `MXL_DOMAIN` | Override domain path in `mxl-demo.sh` |

---

## Flow Discovery

List all available MXL flows in a domain:

```bash
./bin/switchframe --mxl-domain /Volumes/MXL --mxl-discover
```

Output:

```
Available MXL flows in /Volumes/MXL:
  a0000001-...-000000000001  video  1920x1080  29.97fps  active
  a0000001-...-000000000002  audio  48000Hz    stereo    active
  a0000002-...-000000000001  video  1920x1080  29.97fps  active
  a0000002-...-000000000002  audio  48000Hz    stereo    inactive
```

Discovery scans the domain directory for `*.mxl-flow` subdirectories, reads each `flow_def.json` (NMOS IS-04 format), and checks whether a writer is actively publishing.

---

## Source Configuration

### Format

Sources are specified as comma-separated `videoUUID:audioUUID` pairs:

```bash
--mxl-sources "VIDEO_UUID1:AUDIO_UUID1,VIDEO_UUID2:AUDIO_UUID2"
```

Video-only sources (no audio):

```bash
--mxl-sources "VIDEO_UUID1,VIDEO_UUID2"
```

### Example

```bash
./bin/switchframe \
  --mxl-domain /Volumes/MXL \
  --mxl-sources "a0000001-0000-0000-0000-000000000001:a0000001-0000-0000-0000-000000000002,a0000002-0000-0000-0000-000000000001:a0000002-0000-0000-0000-000000000002"
```

### What Happens on Registration

For each MXL source spec:

1. A switcher source is registered with key `mxl:<videoFlowID>` via `RegisterMXLSource()`
2. An audio mixer channel is created with AFV enabled
3. A Prism relay is registered for browser monitoring (H.264/AAC encoded from the raw MXL media)
4. An MXL video flow reader and audio flow reader are opened
5. A `mxl.Source` orchestrator starts, fanning out to switcher, mixer, and browser relay
6. If replay is enabled, the source is added to the replay manager

MXL sources appear in the browser exactly like MoQ sources — with tally indicators, audio level bars, and all standard switching controls.

---

## Program Output

Route the program output back to MXL shared memory:

```bash
./bin/switchframe \
  --mxl-output program \
  --mxl-output-video-def /path/to/output_video.json \
  --mxl-output-audio-def /path/to/output_audio.json
```

The flow definition files describe the output format in NMOS IS-04 JSON. They define the flow UUID, resolution, frame rate, and audio format that will be written to MXL.

### Output Pipeline

- **Video**: The switcher's raw YUV420p program output is converted to V210 and written to MXL at a steady frame rate via a background ticker goroutine
- **Audio**: The mixer's raw float32 PCM output is de-interleaved and written to MXL at the sample rate with monotonic index enforcement
- **Coexistence**: MXL output runs alongside MPEG-TS recording and SRT output — all three can be active simultaneously

---

## How It Works

### MXL Sources vs Standard Sources

| Aspect | Standard (MoQ) | MXL |
|--------|---------------|-----|
| **Input format** | H.264 + AAC | V210 (10-bit 4:2:2) + float32 PCM |
| **Registration** | `RegisterSource(key, relay)` | `RegisterMXLSource(key)` (nil relay) |
| **Video delivery** | Relay viewer callback | `IngestRawVideo()` (raw YUV420p) |
| **Audio delivery** | `IngestFrame()` (AAC, needs decode) | `IngestPCM()` (float32, no decode) |
| **Browser monitoring** | Direct relay subscription | Encoded to H.264/AAC, separate relay |
| **Passthrough optimization** | Available | Not available (raw PCM forces mixing) |

### V210 to YUV420p Conversion

MXL video uses V210 (10-bit packed 4:2:2). The switcher pipeline operates on YUV420p (8-bit planar 4:2:0). The conversion:

1. Extracts 10-bit Y, Cb, Cr values from V210 32-bit words
2. Right-shifts by 2 to convert 10-bit to 8-bit
3. Downsamples chroma 2:1 vertically (4:2:2 → 4:2:0) by averaging adjacent rows
4. Writes to planar Y, U, V buffers

The reverse conversion (`YUV420pToV210`) upsamples chroma for output. Line stride is 128-byte aligned per the V210 specification.

### Writer Steady-Rate Model

The MXL writer does **not** write on every pipeline callback. Instead:

- `WriteVideo()` stores the latest V210 frame atomically
- A background ticker goroutine writes to MXL at the configured grain rate
- This prevents gaps during keyframe waits and bursts during transitions
- Audio uses wall-clock indices with monotonic enforcement to prevent overlapping writes

### Error Recovery

- **Video**: Up to 50 consecutive errors before stopping. Timeout/too-early errors trigger 1ms backoff. Invalid grains (flagged `MXL_GRAIN_FLAG_INVALID`) are skipped.
- **Audio**: "Too late" errors trigger re-sync to ring buffer head. The 5ms read timeout (vs default 100ms) prevents MXL SDK thread starvation.
- **Writer**: Write failures are logged but do not stop output. Resolution mismatches silently drop frames.

---

## Flow Definition Files

MXL uses NMOS IS-04 JSON flow definitions. Example video flow:

```json
{
  "id": "a0000001-0000-0000-0000-000000000001",
  "format": "urn:x-nmos:format:video",
  "label": "Camera 1 Video",
  "media_type": "video/v210",
  "grain_rate": { "numerator": 30000, "denominator": 1001 },
  "frame_width": 1920,
  "frame_height": 1080,
  "interlace_mode": "progressive",
  "colorspace": "BT709",
  "components": [
    { "name": "Y",  "width": 1920, "height": 1080, "bit_depth": 10 },
    { "name": "Cb", "width": 960,  "height": 1080, "bit_depth": 10 },
    { "name": "Cr", "width": 960,  "height": 1080, "bit_depth": 10 }
  ]
}
```

Example audio flow:

```json
{
  "id": "a0000001-0000-0000-0000-000000000002",
  "format": "urn:x-nmos:format:audio",
  "label": "Camera 1 Audio",
  "media_type": "audio/L24",
  "sample_rate": { "numerator": 48000 },
  "sample_count": 1024
}
```

Flow definition files are stored in `test/mxl/` for the demo. For production, create flow definitions that match your MXL-publishing application's configuration.

---

## Troubleshooting

### "MXL support not compiled (build with -tags mxl)"

You built without the `mxl` build tag. Use `make build-server-mxl` or add `-tags "cgo mxl"` to your `go build` command.

### "MXL SDK not found at ..."

Set `MXL_ROOT` to the MXL SDK install directory:

```bash
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release  # macOS
export MXL_ROOT=$HOME/dev/mxl/install/Linux-GCC-Release      # Linux
```

### "MXL domain directory not found"

Create the shared memory domain:

```bash
# macOS
diskutil erasevolume HFS+ MXL $(hdiutil attach -nomount ram://2097152)

# Linux
mkdir -p /dev/shm/mxl
```

### "flow not found" / "flow invalid (writer crashed?)"

- Verify the source application is running and writing to the domain
- Use `--mxl-discover` to list available flows
- Check that UUIDs in `--mxl-sources` match the flow definition files
- The 30-second GC goroutine cleans up stale flows from crashed writers

### "grain expired (too late)"

The reader fell behind the ring buffer. The audio reader auto-resyncs to the write head. For video, this indicates the consumer cannot keep up — check CPU load.

### MXL sources appear but show no video in the browser

MXL sources are encoded to H.264 for browser delivery. Check that FFmpeg/libx264 is available (the encoder probe runs at startup). Look for "video codec selected" in the startup logs.

### Library not found at runtime

Set the library path for MXL shared libraries:

```bash
export DYLD_LIBRARY_PATH=$MXL_ROOT/lib   # macOS
export LD_LIBRARY_PATH=$MXL_ROOT/lib       # Linux
```

---

## Testing

### MXL Pipeline Tests

Run the MXL-specific integration tests (work without the real SDK):

```bash
make test-mxl
```

This runs:
- `TestPipelineVideoRoundTrip` — V210 → YUV420p → V210
- `TestPipelineAudioRoundTrip` — De-interleaved → interleaved → de-interleaved
- `TestPipelineOutputSinkCapture` — Output sink callback verification
- `TestPipelineFullLoopback` — Source → Writer complete loopback
- `TestV210RoundTripIdempotency` — V210 conversion idempotency

### Standard Test Suite

All MXL tests also run as part of the standard test suite (without the `mxl` build tag, using stubs):

```bash
cd server && go test ./... -race
```

The stub implementation verifies that all operations return `ErrMXLNotAvailable` and that mock readers/writers work correctly for unit testing.
