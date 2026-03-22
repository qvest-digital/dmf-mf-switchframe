# GPU Acceleration

GPU-accelerated video processing pipeline for Switchframe. Dual backend: Metal (macOS/Apple Silicon) and CUDA (Linux/NVIDIA). Auto-detected at startup with graceful CPU fallback.

**Contents:** [Overview](#1-overview) · [Architecture](#2-architecture) · [Pipeline Node Chain](#3-pipeline-node-chain) · [Metal](#4-metal-macos) · [CUDA](#5-cuda-linux) · [Compute Shaders](#6-compute-shaders) · [SIMD Assembly Kernels](#7-simd-assembly-kernels) · [AI Inference](#8-ai-inference-tensorrt) · [Build Tags](#9-build-tags) · [Performance](#10-performance)

---

## 1. Overview

When a GPU is available, the entire video processing chain runs on GPU-resident NV12 frames. Frames are uploaded to VRAM once, processed through all pipeline nodes in-place, and downloaded back to CPU only for final output. If no GPU is detected or any GPU node fails, the frame passes through unchanged -- the CPU pipeline handles it transparently.

```
CPU YUV420p ──upload──> GPU NV12 ──[pipeline nodes]──> GPU NV12 ──download──> CPU YUV420p
                                                                    |
                                                              GPU H.264 encode
                                                              (NVENC / VideoToolbox)
```

Backend selection is automatic:

| Platform | Backend | Memory Model |
|----------|---------|--------------|
| macOS (Apple Silicon) | Metal | Unified memory (zero-copy upload/download) |
| macOS (Intel) | Metal | Discrete GPU memory |
| Linux (NVIDIA) | CUDA | Discrete GPU memory with staging buffers |
| Other / no GPU | Stub | Returns `ErrGPUNotAvailable`, CPU pipeline runs |

---

## 2. Architecture

### GPUPipelineNode Interface

GPU pipeline nodes mirror the CPU `switcher.PipelineNode` interface but operate on GPU-resident `GPUFrame` structs:

```go
type GPUPipelineNode interface {
    Name() string
    Configure(width, height, pitch int) error
    Active() bool
    ProcessGPU(frame *GPUFrame) error
    Err() error
    Latency() time.Duration
    Close() error
}
```

All nodes process frames **in-place** on the same `GPUFrame` -- no intermediate copies between nodes. The frame stays in VRAM from upload through encode.

### ProcessingFrame.GPUData

The `ProcessingFrame.GPUData` field (type `any`) carries the GPU frame through the switcher pipeline without circular imports. The `gpu` package imports `switcher` for interfaces; `switcher` does NOT import `gpu`.

### GPUPipeline

`GPUPipeline` holds the configured node chain. Built once via `Build()`, immutable after. Per-node timing is tracked via atomic counters and exposed through `Snapshot()` for the debug endpoint.

```go
pipe := gpu.NewGPUPipeline(ctx, pool)
pipe.Build(1920, 1080, pool.Pitch(), []gpu.GPUPipelineNode{
    uploadNode, keyNode, layoutNode, compositorNode,
    colorGradeNode, aiSegmentNode, stmapNode,
    previewEncodeNode, rawSinkNode, encodeNode,
})
// Per-frame:
pipe.Run(gpuFrame)
```

### Frame Pool

`FramePool` manages GPU-resident NV12 frame buffers. CUDA uses `cuMemAllocPitch` for optimal memory alignment. Metal uses `MTLBuffer` with `StorageModeShared` for unified memory access. Frames are reference-counted (`Ref()` / `Release()`).

---

## 3. Pipeline Node Chain

Frames flow through nodes in this order:

```
upload -> key -> layout -> compositor (DSK) -> colorgrade -> ai_segment -> stmap -> preview_encode -> raw_sink -> encode -> download
```

| Node | Name | Build Tag | Description |
|------|------|-----------|-------------|
| Upload | `gpu_upload` | `darwin` / `cuda` | YUV420p to NV12 conversion and GPU upload |
| Upstream Key | `gpu_key` | `darwin` / `cuda` | Chroma/luma keying with per-source fill frames |
| Layout | `gpu_layout` | `darwin` / `cuda` | PIP/multi-layout compositing (up to 4 slots) |
| DSK Compositor | `gpu_dsk` | `darwin` / `cuda` | Multi-layer graphics overlays (up to 8 layers) |
| Color Grade | `gpu_colorgrade` | `cuda` | 3D LUT color grading with intensity control |
| AI Segment | `gpu_ai_segment` | `cuda && tensorrt` | Person segmentation with background replacement |
| ST Map | `gpu_stmap` | `darwin` / `cuda` | Per-pixel coordinate remapping (lens correction, creative effects) |
| Preview Encode | `gpu_preview_encode` | `darwin` / `cuda` | Low-bitrate preview encoding for browser multiview |
| Raw Sink | `gpu_raw_sink` | `darwin` / `cuda` | Deep-copy callback for MXL output |
| Encode | `gpu_encode` | `darwin` / `cuda` | H.264 hardware encode (NVENC / VideoToolbox) |
| Download | `gpu_download` | `darwin` / `cuda` | NV12 to YUV420p conversion and CPU download |

Each node checks `Active()` dynamically per frame -- inactive nodes are skipped at zero cost. If a node's `ProcessGPU()` returns an error, the pipeline logs a warning and continues with the remaining nodes (graceful degradation).

---

## 4. Metal (macOS)

**File:** `server/gpu/metal_context.go`

### Unified Memory

Apple Silicon has unified memory -- CPU and GPU share the same physical RAM. `MTLBuffer` with `StorageModeShared` gives both CPU and GPU direct pointer access, enabling zero-copy upload and download. This eliminates the PCIe transfer overhead present on discrete GPU architectures.

### Context Initialization

`newMetalContext()` creates the Metal device, command queue, and loads the compiled shader library (`.metallib`). Compute pipeline states are lazily created and cached per kernel function name.

### Metallib Compilation

Metal shaders (`.metal` files) are compiled to a single `switchframe_gpu.metallib` by the Makefile:

```bash
cd server/gpu/metal && make    # Produces switchframe_gpu.metallib
```

`make build` and `make demo` auto-detect macOS and compile the metallib. The binary embeds it via `//go:embed` (`metal_embed_darwin.go`), so no external file is needed at runtime. The `METAL_LIBRARY_PATH` environment variable can override the embedded library.

### Command Queue Architecture

The main context holds one default command queue. Additional queues can be created via `createQueue()` for workflow isolation (e.g., per-preview-encoder). Each queue serializes its own command buffers independently.

---

## 5. CUDA (Linux)

**File:** `server/gpu/context.go`

### Device Initialization

`NewContext()` uses the CUDA Runtime API's primary context via `cudaSetDevice(0)`, which is automatically shared across all OS threads -- critical for cgo where goroutines may migrate between threads.

### Stream Management

Two non-blocking CUDA streams are created at startup:

| Stream | Purpose |
|--------|---------|
| `ctx.stream` | Default processing stream (pipeline nodes) |
| `ctx.encStream` | Encode stream (concurrent with processing) |

Additional streams can be created via `NewStream()` for independent workflows (e.g., per-source segmentation inference). All kernel launches and memory copies use explicit streams -- the null stream is never used because `cudaStreamNonBlocking` streams do not synchronize with it.

### Staging Buffers

Persistent staging buffers for upload/download are lazily allocated on first use and freed in `Close()`. This avoids 180 `cudaMalloc`/`cudaFree` calls per second at 30fps (6 allocs per upload + 6 per download x 30fps). Staging buffers are resized only when frame dimensions change.

### Frame Pool

`FramePool` uses `cuMemAllocPitch` for optimal memory alignment on NVIDIA hardware. Frames are pre-allocated at pipeline build time and managed via reference counting.

### CUDAContext for FFmpeg

`CUDAContext()` returns the current `CUcontext` pointer for use with FFmpeg's CUDA `hw_device_ctx`, enabling hardware-accelerated decode/encode via NVDEC/NVENC on the same device.

---

## 6. Compute Shaders

### Metal Shaders (`server/gpu/metal/*.metal`)

| File | Kernels | Description |
|------|---------|-------------|
| `convert.metal` | `yuv420p_to_nv12`, `nv12_to_yuv420p`, `nv12_fill` | Pixel format conversion between CPU (YUV420p) and GPU (NV12) formats |
| `blend.metal` | `blend_uniform`, `blend_fade_const`, `blend_alpha`, `wipe_mask_generate`, `downsample_alpha_to_nv12_uv` | Transition blending (mix, dip, fade-to-black, wipe, alpha) |
| `scale.metal` | `scale_bilinear`, `scale_bilinear_uv`, `scale_lanczos3_h`, `scale_lanczos3_v` | Bilinear and Lanczos-3 scaling for resolution normalization |
| `key.metal` | `chroma_key_nv12`, `luma_key_nv12` | Upstream chroma/luma key mask generation |
| `composite.metal` | `pip_composite_y`, `pip_composite_uv`, `draw_border_nv12`, `fill_rect_nv12` | PIP layout compositing with border drawing |
| `dsk.metal` | `dsk_overlay_nv12`, `dsk_overlay_rect_nv12` | DSK graphics overlay with per-pixel alpha blending |
| `stmap.metal` | `stmap_warp_y`, `stmap_warp_uv` | ST map per-pixel coordinate remapping with hardware bilinear texture sampling |
| `fruc.metal` | `fruc_blend`, `fruc_interpolate_y`, `fruc_interpolate_uv` | Frame rate up-conversion (MCFI motion-compensated interpolation) |
| `v210.metal` | `v210_to_nv12`, `nv12_to_v210` | V210 (10-bit 4:2:2) conversion for MXL shared-memory transport |

### CUDA Kernels (`server/gpu/cuda/*.cu`)

| File | Kernels | Description |
|------|---------|-------------|
| `convert.cu` | `yuv420p_to_nv12_kernel`, `nv12_to_yuv420p_kernel`, `nv12_fill_kernel` | Pixel format conversion |
| `blend.cu` | `blend_uniform_kernel`, `blend_fade_const_kernel`, `blend_alpha_kernel`, `wipe_mask_kernel`, `downsample_alpha_2x2_kernel`, `downsample_alpha_to_nv12_uv_kernel` | Transition blending |
| `scale.cu` | `scale_bilinear_kernel`, `scale_bilinear_uv_kernel`, `scale_lanczos3_h_kernel`, `scale_lanczos3_v_kernel`, `scale_lanczos3_h_uv_kernel`, `scale_lanczos3_v_uv_kernel` | Bilinear and Lanczos-3 scaling (separate Y and UV kernels) |
| `key.cu` | `chroma_key_nv12_kernel`, `luma_key_nv12_kernel` | Upstream chroma/luma keying |
| `composite.cu` | `pip_composite_y_kernel`, `pip_composite_uv_kernel`, `draw_border_nv12_kernel`, `fill_rect_nv12_kernel` | PIP compositing |
| `dsk.cu` | `alpha_blend_rgba_nv12_kernel`, `alpha_blend_rgba_rect_nv12_kernel` | DSK graphics overlay |
| `stmap.cu` | `stmap_warp_y_kernel`, `stmap_warp_uv_kernel`, `stmap_warp_y_tex_kernel`, `stmap_warp_uv_tex_kernel` | ST map warp (both manual bilinear and hardware texture sampling variants) |
| `fruc.cu` | `fruc_interpolate_y_kernel`, `fruc_interpolate_uv_kernel` | Frame rate up-conversion |
| `v210.cu` | `v210_to_nv12_kernel`, `nv12_to_v210_kernel` | V210 conversion for MXL |
| `colorgrade.cu` | `colorgrade_lut3d_nv12_kernel` | 3D LUT color grading in NV12 domain |
| `preprocess.cu` | `nv12_to_rgb_chw_kernel`, `nv12_to_rgb_nhwc_kernel`, `mask_float_to_u8_scale_kernel` | AI inference preprocessing (NV12 to float32 RGB for TensorRT) |
| `mask_smooth.cu` | `mask_ema_kernel`, `mask_erode_3x3_kernel` | AI mask post-processing (temporal smoothing, morphological erosion) |

---

## 7. SIMD Assembly Kernels

Hand-written assembly kernels for amd64 (SSE/AVX) and arm64 (NEON) platforms, with generic Go fallbacks. These run on the CPU path and are used regardless of GPU availability.

### Transition Blending (`server/transition/`)

| File | Function | Description |
|------|----------|-------------|
| `blend_kernels_amd64.s` | `blendUniform` | YUV420 uniform alpha blend (SSE2/AVX) |
| `blend_kernels_amd64.s` | `blendFadeConst` | Constant fade (fade-to-black) |
| `blend_kernels_amd64.s` | `blendAlpha` | Per-pixel alpha blend |
| `blend_kernels_arm64.s` | `blendUniform` | YUV420 uniform alpha blend (NEON) |
| `blend_kernels_arm64.s` | `blendFadeConst` | Constant fade (NEON) |
| `blend_kernels_arm64.s` | `blendAlpha` | Per-pixel alpha blend (NEON) |
| `downsample_kernels_amd64.s` | `downsampleAlpha2x2` | 2x2 alpha plane downsampling for chroma |
| `downsample_kernels_arm64.s` | `downsampleAlpha2x2` | 2x2 alpha plane downsampling (NEON) |
| `scaler_kernels_amd64.s` | `scaleBilinearRow` | Bilinear scaler row processing |
| `scaler_kernels_arm64.s` | `scaleBilinearRow` | Bilinear scaler row processing (NEON) |
| `scaler_lanczos_kernels_arm64.s` | `lanczosHorizRow` | Lanczos-3 horizontal pass (NEON) |
| `scaler_lanczos_kernels_arm64.s` | `lanczosVertRow` | Lanczos-3 vertical pass (NEON) |

### Graphics Keying (`server/graphics/`)

| File | Function | Description |
|------|----------|-------------|
| `keyer_kernels_amd64.s` | `chromaKeyMaskChroma` | Chroma key mask generation (SSE2) |
| `keyer_kernels_arm64.s` | `chromaKeyMaskChroma` | Chroma key mask generation (NEON) |
| `keyer_luma_kernels_amd64.s` | `lumaKeyMaskLUT` | LUT-based luma key mask |
| `keyer_luma_kernels_arm64.s` | `lumaKeyMaskLUT` | LUT-based luma key mask (NEON) |
| `keymask_blend_amd64.s` | `blendMaskY` | Key mask alpha blending (luma plane) |
| `keymask_blend_arm64.s` | `blendMaskY` | Key mask alpha blending (NEON) |
| `alphablend_kernels_amd64.s` | `alphaBlendRGBARowY` | RGBA overlay alpha blend (Y plane, SSE2) |
| `alphablend_kernels_arm64.s` | `alphaBlendRGBARowY` | RGBA overlay alpha blend (Y plane, NEON) |
| `alphablend_chroma_kernels_amd64.s` | `alphaBlendRGBAChromaRow` | RGBA overlay alpha blend (chroma, SSE2) |
| `alphablend_chroma_kernels_arm64.s` | `alphaBlendRGBAChromaRow` | RGBA overlay alpha blend (chroma, NEON) |
| `expand_mask_arm64.s` | `expandChromaMaskRow` | Chroma mask expansion (NEON) |
| `spill_kernels_arm64.s` | `spillSuppressChroma` | Chroma spill suppression (NEON) |

### Frame Rate Conversion (`server/switcher/frcasm/`)

| File | Function | Description |
|------|----------|-------------|
| `sad_amd64.s` | `SadBlock16x16` | 16x16 block SAD (Sum of Absolute Differences, SSE2) |
| `sad_amd64.s` | `SadBlock16x16x4` | 4-candidate SAD for diamond search |
| `sad_amd64.s` | `SadRow` | Full-row SAD |
| `sad_amd64.s` | `SadBlock16x16HpelH` | Half-pixel horizontal SAD |
| `sad_amd64.s` | `SadBlock16x16HpelV` | Half-pixel vertical SAD |
| `sad_amd64.s` | `SadBlock16x16HpelD` | Half-pixel diagonal SAD |
| `sad_arm64.s` | `SadBlock16x16` | 16x16 block SAD (NEON) |
| `sad_arm64.s` | `SadBlock16x16x4` | 4-candidate SAD (NEON) |
| `sad_arm64.s` | `SadRow` | Full-row SAD (NEON) |
| `sad_arm64.s` | `SadBlock16x16HpelH` | Half-pixel horizontal SAD (NEON) |
| `sad_arm64.s` | `SadBlock16x16HpelV` | Half-pixel vertical SAD (NEON) |
| `sad_arm64.s` | `SadBlock16x16HpelD` | Half-pixel diagonal SAD (NEON) |
| `downsample_amd64.s` | `DownsampleY2x` | 2x luma downsampling for motion estimation |
| `downsample_arm64.s` | `DownsampleY2x` | 2x luma downsampling (NEON) |

### ST Map Warp (`server/stmap/`)

| File | Function | Description |
|------|----------|-------------|
| `warp_kernels_amd64.s` | `warpBilinearRow` | Bilinear warp with 16.16 fixed-point LUTs and PREFETCHT0 |
| `warp_kernels_arm64.s` | `warpBilinearRow` | Bilinear warp with PRFM prefetch (NEON) |

### Audio Vector Operations (`server/audio/vec/`)

| File | Function | Description |
|------|----------|-------------|
| `vec_kernels_amd64.s` | `AddFloat32` | Float32 vector addition (SSE/AVX) |
| `vec_kernels_amd64.s` | `ScaleFloat32` | Float32 vector scaling |
| `vec_kernels_amd64.s` | `MulAddFloat32` | Float32 fused multiply-add |
| `vec_kernels_amd64.s` | `PeakAbsFloat32` | Peak absolute value (mono) |
| `vec_kernels_amd64.s` | `PeakAbsStereoFloat32` | Peak absolute value (stereo interleaved) |
| `vec_kernels_arm64.s` | `AddFloat32` | Float32 vector addition (NEON) |
| `vec_kernels_arm64.s` | `ScaleFloat32` | Float32 vector scaling (NEON) |
| `vec_kernels_arm64.s` | `MulAddFloat32` | Float32 fused multiply-add (NEON) |
| `vec_kernels_arm64.s` | `PeakAbsFloat32` | Peak absolute value (NEON) |
| `vec_kernels_arm64.s` | `PeakAbsStereoFloat32` | Peak absolute stereo (NEON) |

### MXL V210 Conversion (`server/mxl/v210asm/`)

| File | Function | Description |
|------|----------|-------------|
| `v210_kernels_amd64.s` | `ChromaVAvg` | Vertical chroma averaging for 4:2:2 to 4:2:0 |
| `v210_kernels_amd64.s` | `V210UnpackRow` | V210 (10-bit packed) to planar unpack |
| `v210_kernels_amd64.s` | `V210PackRow` | Planar to V210 pack |
| `v210_kernels_arm64.s` | `ChromaVAvg` | Vertical chroma averaging (NEON) |
| `v210_kernels_arm64.s` | `V210UnpackRow` | V210 unpack (NEON) |
| `v210_kernels_arm64.s` | `V210PackRow` | V210 pack (NEON) |

### Replay Audio (`server/replay/`)

| File | Function | Description |
|------|----------|-------------|
| `fft_butterfly_amd64.s` | `butterflyRadix2` | Radix-2 FFT butterfly (SSE2) for phase vocoder |
| `fft_butterfly_arm64.s` | `butterflyRadix2` | Radix-2 FFT butterfly (NEON) |
| `crosscorr_arm64.s` | `crossCorrFloat32` | Float32 cross-correlation for WSOLA (NEON) |

---

## 8. AI Inference (TensorRT)

**Files:** `server/gpu/tensorrt.go`, `server/gpu/segmentation_engine.go`, `server/gpu/node_ai_segment.go`

### U2-Net Segmentation Pipeline

The AI segmentation system uses [U2-Net](https://github.com/xuebinqin/U-2-Net) (u2netp variant, ~4.7 MB ONNX) for real-time person segmentation. The pipeline runs entirely on GPU:

```
Source NV12 frame
  -> nv12_to_rgb_chw (320x320, float32)     [preprocess.cu]
  -> TensorRT inference (u2netp, FP16)       [tensorrt.go]
  -> mask_to_u8_upscale (to source res)      [preprocess.cu]
  -> mask_erode_3x3 (edge cleanup)           [mask_smooth.cu]
  -> mask_ema (temporal smoothing)           [mask_smooth.cu]
  -> store in atomic pointer                 [lock-free read by pipeline]
```

### Per-Source Isolation

Each enabled source gets a dedicated CUDA stream and pre-allocated device buffers. Inference runs asynchronously on the per-source stream, so it does not block the main pipeline stream. The latest mask is stored in an `atomic.Pointer[GPUFrame]` for lock-free reads from the pipeline goroutine.

### Plan Caching

TensorRT serializes an optimized engine plan on first run (~30 seconds for u2netp). The plan is cached at `~/.switchframe/models/` and loaded directly on subsequent starts (sub-second). Plan files are device-specific -- they must be regenerated if the GPU model changes.

### FP16 Mixed Precision

FP16 inference is enabled by default (`TRTEngineOpts.UseFP16 = true`), providing approximately 2x throughput over FP32 with negligible quality loss for segmentation masks.

### Background Modes

The `gpuAISegmentNode` supports three background replacement modes:

| Mode | Description |
|------|-------------|
| `transparent` | No-op pass-through; mask available for downstream keying |
| `blur:N` | Gaussian blur the frame, composite person via mask |
| `color:RRGGBB` | Solid color background, composite person via mask |

---

## 9. Build Tags

| Tag Combination | What It Enables |
|----------------|-----------------|
| (none / `!cgo`) | Stub GPU package: `NewContext()` returns `ErrGPUNotAvailable`. All GPU pipeline nodes return nil. CPU pipeline handles everything. |
| `darwin` | Metal GPU backend. Auto-detected on macOS. Metallib embedded in binary. Zero-copy on Apple Silicon. |
| `cgo && cuda` | CUDA GPU backend. Requires CUDA Toolkit. NVENC hardware encode. Staging buffers for PCIe transfer. |
| `cgo && cuda && tensorrt` | Adds TensorRT AI segmentation. Requires TensorRT SDK (`libnvinfer`, `libnvonnxparser`). |
| `cgo && whisper` | Whisper.cpp speech-to-text engine (not GPU-specific but leverages CUDA when available). |

Build examples:

```bash
# macOS (Metal auto-detected)
go build ./cmd/switchframe

# Linux with CUDA
go build -tags "cgo cuda" ./cmd/switchframe

# Linux with CUDA + AI segmentation
go build -tags "cgo cuda tensorrt" ./cmd/switchframe

# Linux with everything
go build -tags "cgo cuda tensorrt whisper" ./cmd/switchframe

# No GPU (explicit stub)
CGO_ENABLED=0 go build ./cmd/switchframe
```

---

## 10. Performance

### GPU vs CPU Comparison

| Operation | CPU | GPU | Speedup |
|-----------|-----|-----|---------|
| ST map warp (1080p) | ~29 ms | <1 ms | ~30x |
| Transition blend (1080p) | ~8 ms | <0.5 ms | ~16x |
| Chroma key (1080p) | ~5 ms | <0.3 ms | ~17x |
| DSK overlay (1080p) | ~3 ms | <0.2 ms | ~15x |
| Lanczos-3 scale (1080p->480p) | ~12 ms | <1 ms | ~12x |
| AI segmentation (inference) | N/A (CPU not supported) | ~8 ms | -- |
| Preview encode (1080p->480p) | ~15 ms (libx264 ultrafast) | ~2 ms (NVENC/VT) | ~7x |

### Latency Budget

At 1080p29.97 (33.3 ms frame budget), the full GPU pipeline typically completes in under 5 ms total, leaving ample headroom. CPU pipeline latency for the same operations approaches 60-80 ms, which exceeds the frame budget and requires dropping processing nodes.

### Memory Footprint

| Resource | Size (1080p) |
|----------|-------------|
| NV12 frame (GPU) | ~3.1 MB (1920 x 1080 x 1.5, pitched) |
| Frame pool (default 32 frames) | ~100 MB |
| ST map GPU texture (static) | ~16 MB (2 x float32 arrays) |
| AI segmentation per-source buffers | ~12 MB |
| TensorRT engine plan (u2netp) | ~8 MB |

### Monitoring

GPU pipeline metrics are exposed via the debug snapshot endpoint (`GET /api/debug/snapshot`) and include per-node timing (last/max nanoseconds), run count, and total pipeline latency. The `GPUPipeline.Snapshot()` method returns this data:

```json
{
  "gpu": true,
  "active_nodes": [
    {"name": "gpu_key", "last_ns": 280000, "max_ns": 450000, "latency_us": 500},
    {"name": "gpu_dsk", "last_ns": 180000, "max_ns": 310000, "latency_us": 200},
    {"name": "gpu_stmap", "last_ns": 650000, "max_ns": 980000, "latency_us": 500}
  ],
  "run_count": 54321,
  "last_run_ns": 2100000,
  "max_run_ns": 4500000,
  "total_latency_us": 3400
}
```

---

## File Reference

| File | Purpose |
|------|---------|
| [`context.go`](../server/gpu/context.go) | CUDA context, device init, streams, staging buffers |
| [`metal_context.go`](../server/gpu/metal_context.go) | Metal context, command queue, pipeline cache |
| [`stub_context.go`](../server/gpu/stub_context.go) | Non-GPU stub (returns `ErrGPUNotAvailable`) |
| [`pipeline.go`](../server/gpu/pipeline.go) | CUDA `GPUPipeline` struct, `Build()`, `Run()`, per-node timing |
| [`metal_pipeline.go`](../server/gpu/metal_pipeline.go) | Metal `GPUPipeline` (same interface, Metal dispatch) |
| [`pool.go`](../server/gpu/pool.go) | CUDA frame pool (`cuMemAllocPitch`) |
| [`metal_pool.go`](../server/gpu/metal_pool.go) | Metal frame pool (`MTLBuffer`, unified memory) |
| [`upload.go`](../server/gpu/upload.go) | CUDA YUV420p to NV12 upload |
| [`metal_upload.go`](../server/gpu/metal_upload.go) | Metal YUV420p to NV12 upload (zero-copy) |
| [`download.go`](../server/gpu/download.go) | CUDA NV12 to YUV420p download |
| [`metal_download.go`](../server/gpu/metal_download.go) | Metal NV12 to YUV420p download |
| [`node_key.go`](../server/gpu/node_key.go) | GPU upstream key node |
| [`node_layout.go`](../server/gpu/node_layout.go) | GPU layout compositor node |
| [`node_compositor.go`](../server/gpu/node_compositor.go) | GPU DSK graphics overlay node |
| [`node_colorgrade.go`](../server/gpu/node_colorgrade.go) | GPU 3D LUT color grading node (CUDA only) |
| [`node_ai_segment.go`](../server/gpu/node_ai_segment.go) | GPU AI segmentation node (CUDA + TensorRT) |
| [`node_stmap.go`](../server/gpu/node_stmap.go) | GPU ST map warp node |
| [`node_preview_encode.go`](../server/gpu/node_preview_encode.go) | GPU preview encode node |
| [`segmentation_engine.go`](../server/gpu/segmentation_engine.go) | TensorRT segmentation engine |
| [`tensorrt.go`](../server/gpu/tensorrt.go) | TensorRT engine/context wrapper |
| [`colorgrade_cuda.go`](../server/gpu/colorgrade_cuda.go) | CUDA color grading kernel dispatch |
| [`metal_bridge.h`](../server/gpu/metal_bridge.h) | C function declarations for Metal operations |
| [`metal_bridge.m`](../server/gpu/metal_bridge.m) | Objective-C Metal API wrappers |
| [`metal_embed_darwin.go`](../server/gpu/metal_embed_darwin.go) | Embedded metallib via `//go:embed` |
| [`source_manager.go`](../server/gpu/source_manager.go) | GPU source frame management |
