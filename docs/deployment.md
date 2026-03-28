# Deployment

Complete guide to building and deploying Switchframe from a bare server to a fully-featured production instance. Covers every dependency, every build step, and every optional feature.

**Contents:** [Prerequisites](#1-prerequisites) · [Core Dependencies (From Source)](#2-core-dependencies-from-source) · [GPU: CUDA Path](#3-gpu-cuda-path-linux) · [GPU: Metal Path](#4-gpu-metal-path-macos) · [ASR / Speech Recognition](#5-asr--speech-recognition) · [CEF Sidecar (HTML5 Graphics)](#6-cef-sidecar-html5-graphics) · [SMPTE 2110 / DPDK](#7-smpte-2110--dpdk) · [MXL Shared Memory](#8-mxl-shared-memory) · [Building Switchframe](#9-building-switchframe) · [TLS & Certificates](#10-tls--certificates) · [Configuration Reference](#11-configuration-reference) · [Networking & Ports](#12-networking--ports) · [System Tuning](#13-system-tuning) · [Monitoring](#14-monitoring) · [Docker](#15-docker) · [Production Checklist](#16-production-checklist)

---

## 1. Prerequisites

### Operating System

| Platform | Tested | Notes |
|----------|--------|-------|
| Debian 12 (bookworm) | Primary | Production reference platform |
| Ubuntu 24.04 LTS | Yes | Same package ecosystem |
| macOS 14+ (Apple Silicon) | Yes | Metal GPU, unified memory |
| macOS 14+ (Intel) | Yes | Metal GPU, discrete memory |

### Toolchain

| Tool | Minimum | Install |
|------|---------|---------|
| Go | 1.25+ | https://go.dev/dl/ |
| Node.js | 22+ | https://nodejs.org/ (UI build only) |
| GCC / Clang | C11 + C++17 | `apt install build-essential` / Xcode CLI tools |
| pkg-config | any | `apt install pkg-config` / `brew install pkg-config` |
| CMake | 3.19+ | `apt install cmake` (CEF sidecar only) |
| NASM | 2.15+ | `apt install nasm` (FFmpeg x86 assembly) |
| Python 3 | 3.8+ | Required for OptiX PTX shader compilation |

### System Packages (Debian/Ubuntu Baseline)

```bash
apt-get update && apt-get install -y \
  build-essential pkg-config nasm cmake git curl wget \
  libssl-dev zlib1g-dev
```

**macOS:**
```bash
xcode-select --install
brew install pkg-config nasm cmake
```

---

## 2. Core Dependencies (From Source)

Switchframe links against FFmpeg 7.x, SRT, and FDK-AAC at runtime. The Debian-packaged versions are too old — build from source and install to `/usr/local`.

> **Why from source?** Debian 12 ships FFmpeg 5.1 (no NVENC improvements post-2022, missing libsrt/libfdk-aac integration). Building from source gets FFmpeg 7.1 with NVENC, CUDA decode, SRT mux/demux, and FDK-AAC all in one binary.

### 2.1 libsrt (1.5.5+)

SRT is both an FFmpeg dependency and a Go cgo dependency. Build it first.

```bash
cd /tmp
git clone --depth 1 --branch v1.5.5 https://github.com/Haivision/srt.git
cd srt
mkdir build && cd build
cmake .. -DCMAKE_INSTALL_PREFIX=/usr/local \
  -DENABLE_SHARED=ON -DENABLE_STATIC=OFF \
  -DENABLE_APPS=ON -DENABLE_TESTING=OFF
make -j$(nproc)
make install
ldconfig
```

Verify: `pkg-config --modversion srt` → `1.5.5`

### 2.2 libfdk-aac (2.0.3+)

```bash
cd /tmp
git clone --depth 1 --branch v2.0.3 https://github.com/mstorsjo/fdk-aac.git
cd fdk-aac
autoreconf -fiv
./configure --prefix=/usr/local --enable-shared --disable-static
make -j$(nproc)
make install
ldconfig
```

Verify: `pkg-config --modversion fdk-aac` → `2.0.3`

### 2.3 libx264

```bash
cd /tmp
git clone --depth 1 https://code.videolan.org/videolan/x264.git
cd x264
./configure --prefix=/usr/local --enable-shared --disable-static \
  --enable-pic --disable-cli
make -j$(nproc)
make install
ldconfig
```

### 2.4 libopus + libopusfile

```bash
# Debian packages are fine here — API-stable
apt-get install -y libopus-dev libopusfile-dev
```

**macOS:** `brew install opus opusfile`

### 2.5 FFmpeg 7.1

Build with every codec and protocol Switchframe uses. Adjust `--enable-nvenc --enable-cuda` only if you have an NVIDIA GPU (see [Section 3](#3-gpu-cuda-path-linux)).

```bash
cd /tmp
git clone --depth 1 --branch n7.1 https://github.com/FFmpeg/FFmpeg.git
cd FFmpeg

./configure --prefix=/usr/local \
  --enable-gpl --enable-nonfree \
  --enable-libx264 \
  --enable-libfdk-aac \
  --enable-libopus \
  --enable-libsrt \
  --enable-swresample \
  --enable-swscale \
  --enable-shared \
  --disable-static \
  --disable-doc \
  --enable-nvenc \          # NVIDIA only — omit on CPU-only / macOS
  --enable-cuda             # NVIDIA only — omit on CPU-only / macOS

make -j$(nproc)
make install
ldconfig
```

**macOS alternative** (Homebrew builds FFmpeg with most codecs):
```bash
brew install ffmpeg fdk-aac
```

Verify all libraries resolve:
```bash
pkg-config --modversion libavcodec libavformat libavutil libswscale libswresample
# Expected: 61.x, 61.x, 59.x, 8.x, 5.x (FFmpeg 7.1 soversions)
```

### 2.6 Runtime Library Path

If you installed to `/usr/local`, ensure the linker finds them:

```bash
echo "/usr/local/lib" > /etc/ld.so.conf.d/local.conf
ldconfig
```

Verify the switchframe binary links correctly after building:
```bash
ldd /usr/local/bin/switchframe | grep "not found"
# Should print nothing
```

---

## 3. GPU: CUDA Path (Linux)

The CUDA path enables GPU-accelerated video processing where the entire pipeline runs on NV12 frames resident in GPU memory. This is separate from NVENC codec acceleration (which only accelerates H.264 encode/decode).

### What You Get

| Feature | Build Tag | Requirement |
|---------|-----------|-------------|
| GPU compute pipeline (blend, scale, key, composite, warp) | `cuda` | CUDA Toolkit |
| NVENC H.264 encode/decode | `cuda` | CUDA Toolkit + NVIDIA driver |
| AI background segmentation (RVM) | `cuda tensorrt` | + TensorRT SDK |
| OptiX ray tracing / AR rendering | `cuda optix` | + OptiX SDK |
| Neural keying (CorridorKey) | `cuda tensorrt` | + TensorRT SDK + model |

### 3.1 NVIDIA Driver

```bash
# Add NVIDIA apt repo
curl -fsSL https://developer.download.nvidia.com/compute/cuda/repos/debian12/x86_64/cuda-keyring_1.1-1_all.deb -o /tmp/cuda-keyring.deb
dpkg -i /tmp/cuda-keyring.deb
apt-get update

# Install driver
apt-get install -y nvidia-driver cuda-drivers
reboot

# Verify
nvidia-smi
```

### 3.2 CUDA Toolkit (12.6+)

```bash
apt-get install -y cuda-toolkit-12-6
export PATH=/usr/local/cuda/bin:$PATH
```

Add to `/etc/profile.d/cuda.sh`:
```bash
export PATH=/usr/local/cuda/bin:$PATH
```

Verify: `nvcc --version` → `release 12.6`

### 3.3 Build CUDA Kernels

Switchframe has 17 CUDA kernels that compile into a static library:

```bash
cd server/gpu/cuda
make clean all
# Output: libswitchframe_cuda.a (~480KB)
```

The default `ARCH` is `sm_89` (Ada Lovelace / L4). Change it for your GPU:

| GPU | Compute Capability | ARCH flag |
|-----|-------------------|-----------|
| A100 | 8.0 | `-arch=sm_80` |
| A4000 / A5000 | 8.6 | `-arch=sm_86` |
| L4 / RTX 4090 | 8.9 | `-arch=sm_89` (default) |
| H100 | 9.0 | `-arch=sm_90` |

To change: `make ARCH=-arch=sm_86 clean all`

### 3.4 TensorRT (Optional — AI Segmentation + Neural Keying)

```bash
apt-get install -y tensorrt-dev
# Installs libnvinfer, libnvonnxparser, headers
```

Build the TensorRT wrapper:
```bash
cd server/gpu/cuda
make tensorrt
# Output: libswitchframe_tensorrt.a
```

**Models:**

| Model | Size | Purpose | Download |
|-------|------|---------|----------|
| `rvm_mobilenetv3_fp32_fixed.onnx` | ~15 MB | AI background segmentation | Ships in `server/models/` |
| `corridorkey_512.onnx` | ~276 MB | Neural keying | Must be provided separately |

Place models where the `--ai-model` and `--neural-key-model` flags can find them. On first launch, TensorRT builds device-specific engine plans (~30-60s) and caches them at `~/.switchframe/models/`. Plans must be rebuilt if you change GPUs.

### 3.5 OptiX (Optional — AR Rendering)

```bash
# Download OptiX SDK from https://developer.nvidia.com/designworks/optix/downloads/legacy
# Version 9.0.0 tested
chmod +x NVIDIA-OptiX-SDK-9.0.0-linux64-x86_64.sh
./NVIDIA-OptiX-SDK-9.0.0-linux64-x86_64.sh --prefix=/usr/local --skip-license
```

Build the OptiX wrapper:
```bash
cd server/gpu/cuda
make optix HAS_OPTIX=1 OPTIX_INCLUDE=/usr/local/NVIDIA-OptiX-SDK-9.0.0/include
# Output: libswitchframe_optix.a
```

Compile AR PTX shaders:
```bash
server/arrender/shaders/compile_ptx.sh
# Output: server/arrender/shaders/ar_shaders.ptx
```

The compile script accepts environment variables:
- `OPTIX_INCLUDE` — OptiX SDK include path (default: `/usr/local/NVIDIA-OptiX-SDK-8.1.0/include`)
- `CUDA_INCLUDE` — CUDA include path (default: `/usr/local/cuda/include`)
- `ARCH` — GPU architecture (default: `sm_89`)

### 3.6 Trade-offs: CUDA vs CPU

| Dimension | CUDA | CPU-only |
|-----------|------|----------|
| Pipeline latency (1080p) | < 2ms per frame | 30-50ms per frame |
| Max sources at 1080p30 | 8+ | 4 |
| AI segmentation | Real-time (TensorRT) | Not available |
| Encode quality at bitrate | NVENC (good, fixed-function) | libx264 (better, but 5-10x slower) |
| Hardware cost | GPU ($2-5K) | More CPU cores |
| Memory | Separate VRAM pool | Shared system RAM |
| Build complexity | CUDA Toolkit + kernel compilation | None |

---

## 4. GPU: Metal Path (macOS)

Metal is auto-detected on macOS. No manual GPU setup is required.

### What You Get

All GPU pipeline features that CUDA provides (blend, scale, key, composite, warp, encode via VideoToolbox) except TensorRT and OptiX, which are NVIDIA-only.

### 4.1 Build Metal Shaders

```bash
cd server/gpu/metal
make
# Output: switchframe_gpu.metallib
```

This requires `xcrun` (ships with Xcode CLI tools). The Makefile compiles 10 `.metal` shader files into a single `.metallib`, which is embedded in the binary via `//go:embed`.

The root Makefile runs this automatically — `make build` and `make demo` both trigger shader compilation on macOS.

### 4.2 Unified Memory (Apple Silicon)

On Apple Silicon, Metal uses `StorageModeShared` — CPU and GPU share the same physical memory. No staging buffers, no upload/download copies. This makes the GPU pipeline essentially zero-overhead for data transfer.

On Intel Macs, Metal uses discrete GPU memory with explicit transfers (similar to CUDA).

### 4.3 Trade-offs: Metal vs CUDA

| Dimension | Metal (Apple Silicon) | CUDA (NVIDIA) |
|-----------|-----------------------|---------------|
| Data transfer | Zero-copy (unified memory) | PCIe upload/download |
| AI segmentation | Not available | TensorRT |
| OptiX ray tracing | Not available | Available |
| NVENC encode | Not available | Available |
| VideoToolbox encode | Available (high quality) | Not available |
| Neural keying | Not available | Available |
| Target audience | Development, small productions | Production servers |

---

## 5. ASR / Speech Recognition

Switchframe runs ASR as a separate sidecar process (`switchframe-asr`) communicating via POSIX shared memory (audio in) and Unix sockets (transcripts out). Two backends are available.

### 5.1 ONNX Runtime

Required by the Parakeet backend for encoder inference and by the Silero VAD model.

```bash
# Download ONNX Runtime with CUDA EP
cd /opt
wget https://github.com/microsoft/onnxruntime/releases/download/v1.21.1/onnxruntime-linux-x64-gpu-cuda12-1.21.1.tgz
tar xzf onnxruntime-linux-x64-gpu-cuda12-1.21.1.tgz
mv onnxruntime-linux-x64-gpu-cuda12-1.21.1 onnxruntime
```

Add to the library path:
```bash
echo "/opt/onnxruntime/lib" > /etc/ld.so.conf.d/onnxruntime.conf
ldconfig
```

Or set at runtime: `LD_LIBRARY_PATH=/opt/onnxruntime/lib`

### 5.2 Backend A: Parakeet-TDT (GPU, Recommended)

NVIDIA's streaming speech recognition model. Requires CUDA + TensorRT.

**Download the model:**
```bash
# From HuggingFace — Parakeet-TDT 0.6B (ONNX export)
cd /opt/models
git lfs install
git clone https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2-onnx
```

The model directory contains:
- `encoder-model.onnx` + `encoder-model.onnx.data` (~2.3 GB, FP32) or `encoder-model.int8.onnx` (~622 MB, INT8)
- `decoder_joint-model.onnx` (~35 MB) or `decoder_joint-model.int8.onnx` (~8.6 MB)
- `nemo128.onnx` (~137 KB, mel frontend)
- `vocab.txt` (SentencePiece vocabulary)
- `config.json` (model metadata)

Download Silero VAD into the same directory:
```bash
wget -O /opt/models/parakeet-tdt-0.6b-v2-onnx/silero_vad.onnx \
  https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx
```

On first launch, TensorRT builds engine plans from the ONNX files (5-10 minutes for the encoder). Plans are cached and reused on subsequent starts. They must be rebuilt if you change GPUs.

### 5.3 Backend B: Whisper (CPU or GPU)

OpenAI's Whisper model via [whisper.cpp](https://github.com/ggerganov/whisper.cpp). Useful as a fallback or when TensorRT is unavailable.

**Build whisper.cpp with CUDA support:**
```bash
cd /opt
git clone https://github.com/ggerganov/whisper.cpp.git
cd whisper.cpp
mkdir build && cd build
cmake .. -DGGML_CUDA=ON -DCMAKE_BUILD_TYPE=Release
make -j$(nproc)
# Outputs: build/src/libwhisper.a, build/ggml/src/libggml*.a
```

**Download models:**
```bash
cd /opt/whisper.cpp/models
./download-ggml-model.sh base       # 142 MB — fast, lower accuracy
./download-ggml-model.sh medium     # 1.5 GB — recommended for production
./download-ggml-model.sh large-v3   # 2.9 GB — highest accuracy

# VAD model (auto-detected by whisper backend)
./download-vad-model.sh
# Output: ggml-silero-v6.2.0.bin
```

### 5.4 Build the ASR Sidecar

```bash
cd server

# With Parakeet (TensorRT) backend
CGO_CFLAGS="-I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include" \
go build -tags "cuda tensorrt" -o /usr/local/bin/switchframe-asr ./cmd/switchframe-asr

# With Whisper backend only
CGO_CFLAGS="-I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include" \
go build -tags "cuda tensorrt" -o /usr/local/bin/switchframe-asr ./cmd/switchframe-asr
```

### 5.5 Runtime Configuration

```bash
./switchframe --captions \
  --asr-sidecar /usr/local/bin/switchframe-asr \
  --asr-sidecar-model-dir /opt/models/parakeet-tdt-0.6b-v2-onnx \
  --asr-whisper-models-dir /opt/whisper.cpp/models \
  --asr-default-backend parakeet   # or "whisper"
```

The sidecar spawns as a child process. Audio flows via POSIX shared memory ring buffer (`/dev/shm/switchframe-asr-{pid}`). Transcripts return over a Unix socket. Backends can be switched at runtime via the REST API.

### 5.6 Trade-offs: Parakeet vs Whisper

| Dimension | Parakeet-TDT (TensorRT) | Whisper (whisper.cpp) |
|-----------|--------------------------|----------------------|
| Latency | 300-500ms for 3s audio | 2-5s (medium), 5-15s (large) |
| Streaming | Yes (5s sliding windows) | Batch only |
| GPU memory | ~1-2 GB VRAM | Optional (CPU works) |
| Accuracy | Excellent (English) | Excellent (multilingual) |
| Languages | English only (0.6B model) | 99 languages |
| First start | 5-10 min (TRT plan build) | Instant |
| Model size | ~2.3 GB (FP32), 622 MB (INT8) | 142 MB - 2.9 GB |

---

## 6. CEF Sidecar (HTML5 Graphics)

The CEF (Chromium Embedded Framework) sidecar renders HTML/CSS/JavaScript graphics templates into raw BGRA pixel data at up to 30fps. It runs as a separate process communicating over Unix domain sockets.

### 6.1 Install CEF Binary Distribution

```bash
# Download from Spotify CDN
cd /opt
CEF_VERSION="134.3.14+g04c8a25+chromium-134.0.6998.165"
wget "https://cef-builds.spotifycdn.com/cef_binary_${CEF_VERSION}_linux64_minimal.tar.bz2"
tar xjf cef_binary_*.tar.bz2
mv cef_binary_* cef
```

Build the CEF wrapper library:
```bash
cd /opt/cef
mkdir build && cd build
cmake ..
make -j$(nproc) libcef_dll_wrapper
```

### 6.2 Install Display Server (Linux)

CEF requires X11 even for offscreen rendering. Use Xvfb (virtual framebuffer):

```bash
apt-get install -y xvfb \
  libx11-dev libxcomposite-dev libxext-dev libxrandr-dev \
  libasound2-dev libpango1.0-dev libcairo2-dev \
  libdrm-dev libgbm-dev libdbus-1-dev libexpat1-dev \
  libxcb1-dev libxkbcommon-dev libatspi2.0-dev \
  libnss3-dev libnspr4-dev
```

Create systemd service at `/etc/systemd/system/xvfb.service`:
```ini
[Unit]
Description=Xvfb virtual framebuffer for CEF headless rendering
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/Xvfb :99 -screen 0 1920x1080x24
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable --now xvfb
echo 'DISPLAY=:99' >> /etc/environment
```

### 6.3 Build the CEF Sidecar

```bash
mkdir -p /opt/switchframe-cef/build
cd /opt/switchframe-cef/build

cmake -DCEF_ROOT=/opt/cef \
  -DCMAKE_BUILD_TYPE=Release \
  -S /path/to/switchframe/cef-sidecar -B .
make -j$(nproc) switchframe-cef
```

Install:
```bash
mkdir -p /opt/switchframe-cef/bin
cp switchframe-cef /opt/switchframe-cef/bin/
cp -r /path/to/switchframe/cef-sidecar/demo-templates /opt/switchframe-cef/
cp -r /path/to/switchframe/cef-sidecar/resources /opt/switchframe-cef/bin/
```

Create wrapper script at `/usr/local/bin/switchframe-cef`:
```bash
#!/bin/bash
export DISPLAY=:99
cd /opt/switchframe-cef/bin
exec ./switchframe-cef "$@"
```
```bash
chmod +x /usr/local/bin/switchframe-cef
```

### 6.4 Template Formats

CEF supports two template formats:

- **OGraf** — Web components with `playAction()`, `stopAction()`, `updateAction()` methods. Modern approach.
- **CasparCG** — Global `play()`, `stop()`, `update(data)` functions. Legacy compatibility.

18 demo templates ship in `cef-sidecar/demo-templates/`. Format is auto-detected via an injected `adapter.js`.

### 6.5 macOS

On macOS, CEF uses native windowing. Download the macOS CEF distribution and build similarly. No Xvfb needed.

---

## 7. SMPTE 2110 / DPDK

SMPTE ST 2110 support enables professional broadcast I/O over IP multicast using Intel's Media Transport Library (MTL) and DPDK kernel-bypass networking.

> **This is advanced.** ST 2110 requires specific NIC hardware (Intel E810 recommended), DPDK configuration, hugepages, and typically a PTP grandmaster clock. Most deployments won't need this.

### 7.1 DPDK

```bash
# Debian package is sufficient for MTL's needs
apt-get install -y dpdk dpdk-dev
```

Or build from source for latest features:
```bash
cd /tmp
wget https://fast.dpdk.org/rel/dpdk-23.11.3.tar.xz
tar xf dpdk-23.11.3.tar.xz
cd dpdk-stable-23.11.3
meson setup build -Dprefix=/usr/local
cd build && ninja && ninja install
ldconfig
```

### 7.2 Hugepages

DPDK requires hugepages for zero-copy packet buffers.

```bash
# Allocate 1024 x 2MB = 2GB hugepages
echo 'vm.nr_hugepages=1024' >> /etc/sysctl.conf
sysctl -p

# Verify
grep HugePages_Total /proc/meminfo
# HugePages_Total:    1024
```

For GCP or cloud VMs using kernel socket PMD (no NIC passthrough), hugepages can be skipped by using `--st2110-port kernel:ens4`.

### 7.3 Media Transport Library (MTL 24.06+)

```bash
cd /tmp
git clone --depth 1 --branch v24.06 https://github.com/OpenVisualCloud/Media-Transport-Library.git
cd Media-Transport-Library
meson setup build -Dprefix=/usr/local
cd build && ninja && ninja install
ldconfig
```

Verify: `pkg-config --modversion mtl` → `24.06.0.REL`

### 7.4 NMOS Discovery

ST 2110 sources and senders are discoverable via AMWA NMOS IS-04/IS-05. Switchframe implements a built-in NMOS Node server. External controllers (e.g., Sony, Riedel, nmos-cpp) connect to the NMOS API to activate receivers and route streams.

### 7.5 Runtime Configuration

```bash
./switchframe \
  --st2110-port 0000:31:00.0 \    # PCIe BDF of DPDK NIC (or kernel:ethN)
  --st2110-ip 10.0.0.5 \          # Source IP for TX
  --st2110-inputs 4 \             # Number of input receivers
  --st2110-ptp l4 \               # PTP mode: auto, l2, l4, none
  --st2110-nmos-port 8889         # NMOS API port
```

PMD modes:
- `0000:31:00.0` — DPDK PMD (requires VFIO, hugepages, NIC driver binding)
- `kernel:ens4` — Kernel socket PMD (no hugepages, dev/test, GCP-compatible)
- `af_xdp:eth0` — AF_XDP (modern alternative to DPDK PMD)

---

## 8. MXL Shared Memory

MXL provides zero-copy, shared-memory media exchange between processes on the same machine. Used for integration with MXL-compatible tools (playout servers, routers, etc.).

### 8.1 Build MXL SDK

```bash
# Obtain the MXL SDK source
cd /path/to/mxl
mkdir build && cd build
cmake .. -DCMAKE_INSTALL_PREFIX=/usr/local
make -j$(nproc) && make install
ldconfig
```

Verify: `pkg-config --modversion libmxl`

### 8.2 Shared Memory Domain

```bash
# Linux: /dev/shm is already tmpfs
# Nothing to do — MXL uses /dev/shm/mxl by default

# macOS: Create a RAM disk
hdiutil attach -nomount ram://2097152  # 1 GB
diskutil eraseVolume HFS+ MXL /dev/disk4
# Mount point: /Volumes/MXL
```

### 8.3 Build with MXL

```bash
export MXL_ROOT=/usr/local  # or wherever MXL SDK was installed
make build-server-mxl
```

### 8.4 Runtime

```bash
./switchframe \
  --mxl-domain /dev/shm/mxl \
  --mxl-sources "VIDEO_UUID:AUDIO_UUID" \     # colon-separated video:audio:data UUIDs
  --mxl-output program                         # output flow name

# Discover available flows
./switchframe --mxl-discover --mxl-domain /dev/shm/mxl
```

MXL handles V210 (10-bit 4:2:2) video and float32 de-interleaved audio. Switchframe converts V210↔YUV420p using SIMD-accelerated kernels (assembly for amd64/arm64).

---

## 9. Building Switchframe

### 9.1 Build Tag Matrix

| Tag | Enables | Prerequisite |
|-----|---------|--------------|
| `embed_ui` | SvelteKit UI embedded in binary | Node.js + `npm run build` |
| `cuda` | GPU compute pipeline + NVENC | CUDA Toolkit + `libswitchframe_cuda.a` |
| `tensorrt` | AI segmentation + neural keying | `cuda` + TensorRT SDK + `libswitchframe_tensorrt.a` |
| `optix` | OptiX ray tracing / AR rendering | `cuda` + OptiX SDK + `libswitchframe_optix.a` |
| `mxl` | MXL shared-memory transport | MXL SDK (`pkg-config: libmxl`) |
| `st2110` | SMPTE 2110 professional I/O | MTL + DPDK (`pkg-config: mtl`) |
| `openh264` | OpenH264 fallback codec | libopenh264 |
| `noffmpeg` | Disables all FFmpeg bindings | — |
| *(macOS auto)* | Metal GPU pipeline | macOS + `xcrun` |

### 9.2 Development Build

```bash
make build-server    # Go binary only, no UI embed (use Vite dev server)
cd ui && npm run dev # SvelteKit dev server with API proxy
```

### 9.3 Production Build (CPU-only)

```bash
# Build UI
cd ui && npm ci && npm run build && cd ..

# Build binary with embedded UI
make build
# Output: bin/switchframe
```

### 9.4 Production Build (Full CUDA)

This is the build used on the production server. Builds everything: CUDA kernels, TensorRT wrapper, OptiX wrapper, PTX shaders, UI, and the Go binary.

```bash
export PATH=/usr/local/cuda/bin:$PATH

# 1. Build CUDA kernels + optional wrappers
cd server/gpu/cuda
make clean all tensorrt optix HAS_OPTIX=1 OPTIX_INCLUDE=/usr/local/NVIDIA-OptiX-SDK-9.0.0/include

# 2. Compile OptiX PTX shaders
server/arrender/shaders/compile_ptx.sh

# 3. Build UI
cd ui && npm ci && npm run build && cd ..

# 4. Copy UI build into embed directory
rm -rf server/cmd/switchframe/ui
cp -r ui/build server/cmd/switchframe/ui

# 5. Build Go binary
cd server
CGO_LDFLAGS="-L/usr/local/lib -L/usr/lib/x86_64-linux-gnu" \
CGO_CFLAGS="-I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include" \
go build -tags "embed_ui cuda tensorrt optix" -o /usr/local/bin/switchframe ./cmd/switchframe
```

### 9.5 Build the ASR Sidecar

```bash
cd server
CGO_CFLAGS="-I/opt/whisper.cpp/include -I/opt/whisper.cpp/ggml/include" \
go build -tags "cuda tensorrt" -o /usr/local/bin/switchframe-asr ./cmd/switchframe-asr
```

### 9.6 Verify Linked Libraries

After building, confirm all shared libraries resolve:

```bash
ldd /usr/local/bin/switchframe | grep "not found"
# Expected: empty (nothing missing)
```

Expected libraries in the output:
```
libavcodec.so.61      → /usr/local/lib/     (FFmpeg)
libavformat.so.61     → /usr/local/lib/
libavutil.so.59       → /usr/local/lib/
libswscale.so.8       → /usr/local/lib/
libswresample.so.5    → /usr/local/lib/
libfdk-aac.so.2       → /usr/local/lib/     (AAC codec)
libsrt.so.1.5         → /usr/local/lib/     (SRT)
libx264.so.164        → system              (H.264 encoder)
libopus.so.0          → system              (voice comms)
libcuda.so.1          → system              (CUDA, if GPU build)
libcudart.so.12       → /usr/local/cuda/    (CUDA runtime, if GPU build)
libnvinfer.so.10      → system              (TensorRT, if tensorrt build)
libnvonnxparser.so.10 → system              (TensorRT, if tensorrt build)
```

---

## 10. TLS & Certificates

Switchframe serves everything over QUIC/HTTP3, which requires TLS.

### Self-Signed (Default)

At startup, a 14-day self-signed certificate is generated automatically. Browsers use `serverCertificateHashes` (WebTransport fingerprint pinning) to trust it. The fingerprint is available unauthenticated at `/api/cert-hash` (admin port 9090).

Good for development and demos. Browsers must refetch the fingerprint on each server restart.

### mkcert (Local Development)

Generates locally-trusted certificates so browsers connect directly over HTTP/3 without pinning:

```bash
make setup-mkcert
./bin/switchframe --tls-cert ~/.switchframe/cert.pem --tls-key ~/.switchframe/key.pem
```

### Let's Encrypt (Production)

For production with a domain name:

```bash
apt-get install -y certbot

# Wildcard cert via DNS challenge (required for subdomains)
certbot certonly --manual --preferred-challenges dns \
  -d switchframe.dev -d '*.switchframe.dev'

# Point switchframe at the certs
./switchframe \
  --tls-cert /etc/letsencrypt/live/switchframe.dev/fullchain.pem \
  --tls-key /etc/letsencrypt/live/switchframe.dev/privkey.pem
```

Set up auto-renewal:
```bash
certbot renew --dry-run
# Certbot installs a systemd timer automatically
```

When a trusted cert is used, `/api/cert-hash` returns `"trusted": true` and browsers skip fingerprint pinning.

### Reverse Proxy (Caddy)

For production deployments behind a reverse proxy:

```
┌──────────┐  HTTPS :443   ┌───────┐  HTTP :8081   ┌─────────────┐
│  Browser  │──────────────▶│ Caddy │──────────────▶│ Switchframe │
│           │               │       │               │ (REST API)  │
│           │  QUIC :8080   │       │               │             │
│           │──────────────────────────────────────▶│ (WebTransport)
└──────────┘                └───────┘               └─────────────┘
```

Caddy terminates TLS for REST API traffic. WebTransport/QUIC traffic goes directly to port 8080 (QUIC cannot be proxied — it's UDP end-to-end). Run switchframe with `--http-fallback` to expose the REST API on TCP `:8081`.

---

## 11. Configuration Reference

### CLI Flags

#### Core

| Flag | Default | Description |
|------|---------|-------------|
| `--demo` | `false` | 4 simulated cameras, auth disabled, SRT listener auto-enabled |
| `--log-level` | `info` | `debug` · `info` · `warn` · `error` |
| `--admin-addr` | `:9090` | Admin/metrics server listen address |
| `--api-token` | auto | Bearer token for API auth |
| `--http-fallback` | `false` | Plain HTTP/1.1 API on TCP `:8081` |
| `--tls-cert` | — | TLS certificate PEM path |
| `--tls-key` | — | TLS private key PEM path |

#### Video Pipeline

| Flag | Default | Description |
|------|---------|-------------|
| `--format` | `1080p29.97` | Video standard preset (see [Format Presets](#format-presets) below) |
| `--color-format` | `standard` | Pipeline color format profile (see [Color Format Profiles](#color-format-profiles) below) |
| `--frame-sync` | `false` | Enable freerun frame synchronizer (aligns sources to common tick) |
| `--frc-quality` | `none` | Frame rate conversion: `none` · `nearest` · `blend` · `mcfi` |
| `--replay-buffer-secs` | `60` | Per-source replay buffer (0 = disabled, max 300) |
| `--captions` | `false` | Enable CEA-608/708 closed captioning |
| `--preview-proxy` | `false` | Low-bitrate browser previews (480p) |
| `--preview-resolution` | `480p` | Preview encode resolution |
| `--preview-bitrate` | `500000` | Preview encode bitrate (bps) |
| `--relay-bitrate` | `2000000` | SRT source relay encode bitrate (bps) |
| `--relay-resolution` | `720p` | Relay encode resolution |

#### SRT

| Flag | Default | Description |
|------|---------|-------------|
| `--srt-listen` | — | SRT listener address (e.g., `:6464`) |
| `--srt-latency` | `120` | Default SRT latency (ms, 0-10000) |
| `--srt-output-ports` | — | Output port range (e.g., `7464-7467`) |

#### SCTE-35 / SCTE-104

| Flag | Default | Description |
|------|---------|-------------|
| `--scte35` | `false` | Enable SCTE-35 insertion into MPEG-TS output |
| `--scte35-pid` | `258` | MPEG-TS PID for SCTE-35 |
| `--scte35-preroll` | `4000` | Pre-roll (ms) |
| `--scte35-heartbeat` | `5000` | splice_null heartbeat interval (ms, 0 = disabled) |
| `--scte35-webhook` | — | URL for async event notifications |
| `--scte104` | `false` | SCTE-104 on MXL data flows (requires `--scte35` + MXL build) |

#### MXL

| Flag | Default | Description |
|------|---------|-------------|
| `--mxl-sources` | — | Source specs: `videoUUID[:audioUUID[:dataUUID]],...` |
| `--mxl-output` | — | MXL flow name for program output |
| `--mxl-domain` | `/dev/shm/mxl` | Shared memory domain path |
| `--mxl-discover` | `false` | List available flows and exit |

#### ST 2110

| Flag | Default | Description |
|------|---------|-------------|
| `--st2110-port` | — | DPDK BDF or `kernel:ethN` or `af_xdp:ethN` |
| `--st2110-ip` | — | Source IP for TX |
| `--st2110-inputs` | `4` | Number of input receivers |
| `--st2110-ptp` | `auto` | PTP mode: `auto` · `l2` · `l4` · `none` |
| `--st2110-nmos-port` | `8090` | NMOS Node/Connection API port |

#### ASR / AI

| Flag | Default | Description |
|------|---------|-------------|
| `--asr-sidecar` | — | Path to `switchframe-asr` binary |
| `--asr-sidecar-model-dir` | — | Parakeet model directory |
| `--asr-whisper-models-dir` | — | Whisper GGML model directory |
| `--asr-default-backend` | `parakeet` | Default ASR backend |
| `--ai-model` | — | ONNX model path for AI background segmentation |
| `--neural-key-model` | — | ONNX model path for neural keying |
| `--cache-dir` | platform | Persistent cache for LUTs and decoded assets |

#### Playout

| Flag | Default | Description |
|------|---------|-------------|
| `--playout` | `false` | Enable playout engine |
| `--playout-cache-max` | `200` | Max cache size (GB) |
| `--playout-asrun-retention` | `30` | As-run log retention (days) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SWITCHFRAME_API_TOKEN` | API bearer token (overridden by `--api-token`) |
| `SWITCHFRAME_ADMIN_TOKEN` | Admin server bearer token |
| `SWITCHFRAME_STATE_DIR` | State directory (default: `~/.switchframe`) |
| `SWITCHFRAME_INVITE_TOKENS` | Operator tokens: `role:token,role:token,...` |
| `APP_ENV` | `production` for JSON structured logging |
| `GOGC` | GC target % (defaults to `400` if unset) |
| `GOMEMLIMIT` | Go memory limit (defaults to `2GB` if unset) |
| `METAL_LIBRARY_PATH` | Override Metal shader library path (macOS) |
| `LD_LIBRARY_PATH` | Add paths for ONNX Runtime or other libs |

### Format Presets

| 2160p (4K) | 1080p | 720p |
|------------|-------|------|
| `2160p60` · `2160p59.94` | `1080p60` · `1080p59.94` | `720p60` · `720p59.94` |
| `2160p50` | `1080p50` | `720p50` |
| `2160p30` · `2160p29.97` | `1080p30` · **`1080p29.97`** (default) | `720p30` · `720p29.97` |
| `2160p25` | `1080p25` | `720p25` |
| — | `1080p24` · `1080p23.976` | — |

Frame rates use rational numbers for broadcast correctness (e.g., 30000/1001 for 29.97fps NTSC). The format can also be changed at runtime via `PUT /api/format`.

### Color Format Profiles

The `--color-format` flag selects the pipeline's internal pixel format and output codec. Set at startup; cannot be changed at runtime.

| Profile | Pixel Format | Codec | Use Case |
|---------|-------------|-------|----------|
| `standard` | YUV420 8-bit | H.264 (AVC) | Default. Maximum compatibility with browsers, CDNs, and decoders. |
| `standard-hevc` | YUV420 8-bit | H.265 (HEVC) | Same quality at ~40% lower bitrate. Requires HEVC-capable encoder. |
| `professional` | YUV422 10-bit | H.265 (HEVC) | Broadcast-grade color fidelity. 4:2:2 chroma, 10-bit depth, HEVC encoding. |

`professional` requires an HEVC-capable encoder: `hevc_nvenc` (NVIDIA), `hevc_videotoolbox` (macOS), or `libx265` (software). The encoder is auto-detected at startup via `ProbeEncoders()`.

Browser preview and multiview always use H.264 8-bit regardless of the pipeline color format -- browsers do not support HEVC in WebCodecs.

MXL V210 ingest is lossless in `professional` mode: both V210 and YUV422P10 are 10-bit 4:2:2, so no chroma subsampling or bit depth conversion occurs.
---

## 12. Networking & Ports

### Port Summary

| Port | Protocol | Direction | Purpose |
|------|----------|-----------|---------|
| **8080** | UDP | Inbound | QUIC/HTTP3, WebTransport, MoQ, REST API, UI |
| **8080** | TCP | Inbound | HTTP/3 Alt-Svc (browsers discover QUIC via TCP) |
| **8081** | TCP | Inbound | Plain HTTP REST API (opt-in via `--http-fallback`) |
| **9090** | TCP | Inbound | Admin: metrics, health, pprof, cert-hash |
| **6464** | UDP | Inbound | SRT listener (encoder push, `--srt-listen`) |
| **9000** | UDP | Inbound | SRT output listener (downstream pull) |
| *Ephemeral* | UDP | Outbound | SRT caller (pull sources, output push) |

### Firewall Rules

```bash
# Required: WebTransport / MoQ (browser clients)
iptables -A INPUT -p udp --dport 8080 -j ACCEPT
iptables -A INPUT -p tcp --dport 8080 -j ACCEPT

# Optional: Plain HTTP API (only if --http-fallback)
iptables -A INPUT -p tcp --dport 8081 -j ACCEPT

# Restrict: Admin (monitoring network only — exposes pprof)
iptables -A INPUT -p tcp --dport 9090 -s 10.0.0.0/8 -j ACCEPT

# Optional: SRT input
iptables -A INPUT -p udp --dport 6464 -j ACCEPT

# Optional: SRT output
iptables -A INPUT -p udp --dport 9000 -j ACCEPT
```

---

## 13. System Tuning

### File Descriptors

Each WebTransport connection, SRT connection, and file handle consumes a file descriptor. Raise the limit:

```bash
# /etc/security/limits.conf
* soft nofile 65536
* hard nofile 65536

# Or per-service in systemd
LimitNOFILE=65536
```

### Network Buffers

For SRT and QUIC throughput:

```bash
# /etc/sysctl.conf
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.core.rmem_default = 262144
net.core.wmem_default = 262144
```

```bash
sysctl -p
```

### Go Runtime

Switchframe auto-tunes these if not set:

| Variable | Default | Purpose |
|----------|---------|---------|
| `GOGC=400` | 400% | Reduces GC frequency (trades memory for CPU). Real-time video can't afford GC pauses. |
| `GOMEMLIMIT=2GB` | 2 GB | Soft memory ceiling. Increase for many sources or large replay buffers. |

### Shared Memory (tmpfs)

For replay buffers and ephemeral caches:

```bash
# /dev/shm is typically 50% of RAM by default
# Verify it's large enough for your replay buffer config
df -h /dev/shm

# To increase: add to /etc/fstab
tmpfs /dev/shm tmpfs defaults,size=16G 0 0
mount -o remount /dev/shm
```

### Persistent Cache Directory

For mmap'd LUT caches and decoded assets (survives restarts):

```bash
mkdir -p /data/switchframe/cache
chown switchframe:switchframe /data/switchframe/cache
# Pass via --cache-dir /data/switchframe/cache
```

---

## 14. Monitoring

### Prometheus Metrics

Scraped at `http://localhost:9090/metrics`.

**HTTP:** `switchframe_http_requests_total{method,pattern,status}`, `switchframe_http_request_duration_seconds{method,pattern}`

**Switcher:** `switchframe_cuts_total`, `switchframe_transitions_completed_total{type}`, `switchframe_idr_gate_events_total`

**Audio:** `switchframe_mixer_frames_mixed_total`, `switchframe_mixer_encode_errors_total`

**Output:** `switchframe_output_ringbuf_overflows_total`, `switchframe_output_srt_reconnects_total`, `switchframe_output_recording_bytes_total`, `switchframe_output_srt_bytes_total`

**Pipeline:** `switchframe_pipeline_frames_processed_total`, `switchframe_pipeline_node_duration_seconds{node}`, `switchframe_pipeline_decode_duration_seconds`, `switchframe_pipeline_encode_duration_seconds`

**Source Health:** `switchframe_source_status_changes_total{source,from_status,to_status}`

Go runtime (`go_goroutines`, `go_memstats_*`, `go_gc_*`) and process (`process_cpu_seconds_total`, `process_resident_memory_bytes`, `process_open_fds`) are included automatically.

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: switchframe
    scrape_interval: 15s
    static_configs:
      - targets: ["switchframe:9090"]
```

### Health Checks

| Endpoint | Port | Response | Purpose |
|----------|------|----------|---------|
| `/health` | 9090 | Always 200 | Liveness probe |
| `/ready` | 9090 | 503 during startup, 200 when ready | Readiness probe |

### pprof

```bash
go tool pprof http://localhost:9090/debug/pprof/profile?seconds=30  # CPU
go tool pprof http://localhost:9090/debug/pprof/heap                # Memory
curl http://localhost:9090/debug/pprof/goroutine?debug=2            # Goroutines
```

### Debug Snapshot

```bash
curl -H "Authorization: Bearer $TOKEN" https://localhost:8080/api/debug/snapshot
```

Returns JSON with all subsystem state, circular event log (100 entries), and uptime.

---

## 15. Docker

### Three-Stage Build

```bash
make docker
# or: docker build -t switchframe .
```

Stage 1 (node:22) builds the SvelteKit UI. Stage 2 (golang:1.25) compiles the Go binary with embedded UI. Stage 3 (debian:bookworm-slim) contains only the binary and runtime shared libraries.

> **Note:** The Docker build produces a CPU-only image with Debian-packaged FFmpeg (older). For GPU or source-built FFmpeg, build natively per the instructions above.

### Docker Run

```bash
docker run -d \
  --name switchframe \
  -p 8080:8080/udp -p 8080:8080/tcp \
  -p 9090:9090/tcp \
  -p 6464:6464/udp \
  -e SWITCHFRAME_API_TOKEN=your-secret-token \
  -e APP_ENV=production \
  -v switchframe-data:/home/switchframe/.switchframe \
  switchframe \
    --log-level info --http-fallback
```

For NVIDIA GPU passthrough:
```bash
docker run --gpus 1 ...
```

### Docker Compose

```yaml
services:
  switchframe:
    image: switchframe:latest
    build: .
    ports:
      - "8080:8080/udp"
      - "8080:8080/tcp"
      - "8081:8081/tcp"
      - "9090:9090/tcp"
      - "6464:6464/udp"
      - "9000:9000/udp"
    environment:
      SWITCHFRAME_API_TOKEN: "${SWITCHFRAME_API_TOKEN}"
      APP_ENV: production
    volumes:
      - config:/home/switchframe/.switchframe
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9090/health"]
      interval: 30s
      timeout: 5s
      retries: 3
    restart: unless-stopped

volumes:
  config:
```

---

## 16. Production Checklist

### Security

- [ ] Set `SWITCHFRAME_API_TOKEN` explicitly (don't rely on auto-generated)
- [ ] Set `APP_ENV=production` for structured JSON logging
- [ ] Restrict port 9090 to internal networks (exposes pprof)
- [ ] Use CA-signed TLS certs (`--tls-cert` / `--tls-key`) or a reverse proxy
- [ ] Never run `--demo` in production (disables auth)
- [ ] Set `--log-level info` (not `debug`)

### Resources

- [ ] Set `ulimit -n 65536` (or `LimitNOFILE=65536` in systemd)
- [ ] Tune network buffers (`net.core.rmem_max`, `net.core.wmem_max`)
- [ ] Verify `/dev/shm` is large enough for replay buffers
- [ ] Mount persistent storage for `~/.switchframe/` (state, presets, clips)
- [ ] If using `--cache-dir`, ensure the directory exists and is writable

### Video Pipeline

- [ ] Verify codec auto-detection at startup (`level=INFO msg="video codec selected"`)
- [ ] Confirm GPU detection if using CUDA or Metal build
- [ ] Set `--format` to match your production standard
- [ ] Enable `--frame-sync` for mixed-rate sources
- [ ] Enable `--preview-proxy` for >4 sources (reduces browser CPU)
- [ ] Size `--replay-buffer-secs` based on available RAM (~150 MB/source/min at 1080p)

### SRT

- [ ] Set `--srt-listen` if accepting encoder push connections
- [ ] Open UDP port in firewall
- [ ] Tune `--srt-latency` for your network (higher = more resilient, more delay)

### ASR (if enabled)

- [ ] Verify `switchframe-asr` binary exists at configured path
- [ ] Verify model files exist (Parakeet directory or Whisper GGML files)
- [ ] Set `LD_LIBRARY_PATH` for ONNX Runtime if not in standard paths
- [ ] First Parakeet launch builds TRT plans (5-10 min) — do this before going live

### Monitoring

- [ ] Configure Prometheus scraping on port 9090
- [ ] Set up alerting on `ringbuf_overflows` (output backpressure)
- [ ] Set up alerting on `srt_reconnects` (network instability)
- [ ] Test `/ready` readiness probe
- [ ] Plan certificate renewal schedule

### State & Persistence

Switchframe stores state in `SWITCHFRAME_STATE_DIR` (default: `~/.switchframe/`):

```
~/.switchframe/
├── presets.json            # Switcher state snapshots
├── macros.json             # Macro definitions
├── operators.json          # Registered operators
├── scte35_rules.json       # SCTE-35 signal conditioning rules
├── layout_presets.json     # PIP layout definitions
├── srt_sources.json        # SRT source configurations
├── stingers/               # Uploaded stinger clips (PNG sequences + WAV)
├── clips/                  # Uploaded media clips
├── models/                 # TensorRT engine plan cache
├── colorgrade/             # Custom 3D LUT files
├── stmaps/                 # ST map coordinate files
├── cert.pem, key.pem       # mkcert certificates (if generated)
└── recordings/             # Recorded .ts files
```

Back up this directory. The `models/` subdirectory contains device-specific TensorRT plans that are not portable between GPUs — they'll be regenerated on a new machine.
