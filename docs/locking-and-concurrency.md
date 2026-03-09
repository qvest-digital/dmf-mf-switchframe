# Locking & Concurrency Model

> How Switchframe routes frames through the pipeline without dropping them,
> and the lock hierarchy that makes it safe.

---

## Architecture Overview

Switchframe processes video at 30–60 fps across multiple goroutines. The design
principle is: **locks protect state, channels transport frames, atomics track
metrics.** No lock is held while doing expensive work (decode, blend, encode).

```mermaid
graph TD
    subgraph "Per-Source Goroutines"
        SV1[sourceViewer 1]
        SV2[sourceViewer 2]
        SV3[sourceViewer N]
    end

    subgraph "Synchronization Layer"
        FS[FrameSynchronizer]
        DB[DelayBuffer]
    end

    subgraph "Per-Source Decoders"
        SD1[sourceDecoder 1]
        SD2[sourceDecoder 2]
        SD3[sourceDecoder N]
    end

    subgraph "Switcher Core"
        HRVF[handleRawVideoFrame]
        HAF[handleAudioFrame]
        TE[TransitionEngine]
    end

    subgraph "Async Pipeline"
        VPC[videoProcCh ‹channel›]
        VPL[videoProcessingLoop]
        PC[pipelineCodecs]
    end

    subgraph "Output"
        PR[programRelay]
        OV[OutputViewer]
        MX[TSMuxer]
        AA[AsyncAdapter]
        SRT[SRT / Recorder]
    end

    subgraph "Audio"
        MIX[AudioMixer]
    end

    SV1 -->|atomic.Pointer| SD1
    SV2 -->|atomic.Pointer| SD2
    SV3 -->|atomic.Pointer| SD3
    SD1 --> FS
    SD2 --> DB
    SD3 --> DB
    FS -->|callback| HRVF
    DB -->|callback| HRVF
    FS -->|callback| HAF
    DB -->|callback| HAF
    HRVF -->|RLock| TE
    HRVF --> VPC
    TE -->|blend| VPC
    VPC --> VPL
    VPL --> PC
    PC --> PR
    PR --> OV
    OV -->|channel| MX
    MX --> AA
    AA -->|channel| SRT
    HAF --> MIX
    MIX --> PR
```

---

## Lock Inventory

Every lock in the system, what it protects, and its characteristics:

| Component | Field | Type | Protects | Hot Path? |
|-----------|-------|------|----------|-----------|
| Switcher | `s.mu` | `RWMutex` | sources map, programSource, state, transEngine, pipeCodecs | Yes (RLock) |
| FrameSynchronizer | `fs.mu` | `Mutex` | sources map, tickRate, tickNum | Yes (brief) |
| syncSource | `ss.mu` | `Mutex` | per-source ring buffers (video/audio) | Yes (brief) |
| DelayBuffer | `db.mu` | `Mutex` | sources map | Conditional |
| sourceDecoder | (none) | — | Single goroutine per decoder, channel-based | Yes |
| pipelineCodecs | `pc.mu` | `Mutex` | encoder state, avc1Buf | Yes (brief) |
| TransitionEngine | `e.mu` | `RWMutex` | state, decoders, YUV buffers, blender | During transitions |
| AudioMixer | `m.mu` | `RWMutex` | channels, mix state, crossfade | Yes |
| TSMuxer | `m.mu` | `Mutex` | muxer, output buffer | Yes |
| OutputManager | `m.mu` | `Mutex` | viewer/muxer lifecycle, adapters | Config only |
| OutputDestination | `dest.mu` | `Mutex` | adapter, active state | Config only |
| ConfidenceMonitor | `cm.mu` | `RWMutex` | JPEG thumbnail, decoder | 1 fps |
| healthMonitor | `hm.mu` | `RWMutex` | source status map | Periodic |

### Lock-Free Components

| Component | Field | Type | Purpose |
|-----------|-------|------|---------|
| sourceViewer | `delayBuffer` | `atomic.Pointer` | Hot-swap delay buffer / frame sync |
| sourceViewer | `frameSync` | `atomic.Pointer` | Hot-swap frame synchronizer |
| sourceViewer | `srcDecoder` | `atomic.Pointer` | Per-source H.264→YUV decoder |
| sourceViewer | `videoSent` etc. | `atomic.Int64` | Per-source counters (cache-line padded) |
| Switcher | `rawMonitorSink` | `atomic.Pointer` | Raw YUV program monitor tap (deep copy to MoQ track) |
| DelayBuffer | `stopped` | `atomic.Bool` | Lock-free check in timer callbacks |
| sourceDelay | `generation` | `atomic.Uint64` | Invalidate in-flight timer callbacks |
| DelayBuffer | `hasAnyDelay` | `atomic.Bool` | Skip lock when no sources have delay |
| OutputManager | `adapters` | `atomic.Pointer` | Lock-free read in muxer callback |
| Switcher | 30+ fields | `atomic.Int64` etc. | Metrics counters (never locked) |

### Pools

| Pool | Location | Seed Size | Purpose |
|------|----------|-----------|---------|
| `yuvPool` | `processing_frame.go` | 1080p (3.1 MB) | YUV420 frame buffers |
| `avc1Pool` | `pipeline_codecs.go` | 64 KB | Encoded AVC1 output |
| `tsPacketPool` | `async_adapter.go` | 64 KB | MPEG-TS packet batches |
| `lanczosIntermPool` | `scaler_lanczos.go` | 1080p (5.5 MB) | Lanczos horizontal pass float32 intermediates |

### Atomic Caches

| Cache | Location | Size | Purpose |
|-------|----------|------|---------|
| `kernelCache` | `scaler_lanczos.go` | 8 entries | Precomputed Lanczos-3 kernel weights (keyed by src/dst size) |

---

## Frame Flow Diagrams

### Flow 1: Normal Video Frame (No Transition)

The most common path — a single source is on program, frames flow straight through.
With always-decode, each source has a dedicated decoder goroutine. Decoded YUV
frames pass through the delay buffer or frame sync, then to `handleRawVideoFrame`.

```mermaid
sequenceDiagram
    participant SV as sourceViewer
    participant SD as sourceDecoder
    participant DB as DelayBuffer
    participant SW as Switcher
    participant CH as videoProcCh
    participant PL as videoProcessingLoop
    participant PC as pipelineCodecs
    participant PR as programRelay

    Note over SV: atomic.Pointer load srcDecoder
    SV->>SD: Send(frame) [channel, cap=2]
    Note over SD: Decode goroutine:<br/>AVC1→AnnexB<br/>decoder.Decode() → YUV420<br/>Deep-copy from pool

    SD->>DB: handleRawVideoFrame(key, pf)
    Note over DB: atomic hasAnyDelay check<br/>If delay=0: no lock, passthrough

    DB->>SW: handleRawVideoFrame(key, pf)
    activate SW
    Note over SW: s.mu.RLock()<br/>Read: sources, programSource,<br/>state, transEngine<br/>s.mu.RUnlock()
    deactivate SW

    SW->>CH: enqueueVideoWork (channel send)
    Note over CH: Buffered channel (cap=8)<br/>Newest-wins drop policy

    CH->>PL: work := <-videoProcCh
    PL->>PC: encode(pf, isKF)
    activate PC
    Note over PC: pc.mu.Lock() — config<br/>pc.mu.Unlock()<br/>Encode (no lock)
    deactivate PC

    PL->>PR: BroadcastVideo(frame)
    Note over PR: Copies data to each viewer's channel
```

**Lock sequence:** `s.mu` RLock → release → `pc.mu` Lock → release

**Total locks on hot path:** 2 mutex acquisitions, all brief, none nested.

---

### Flow 2: Video Frame During Transition

Two sources feed the transition engine with pre-decoded YUV; blended output goes through the async pipeline.

```mermaid
sequenceDiagram
    participant SD as sourceDecoder (A or B)
    participant SW as Switcher
    participant TE as TransitionEngine
    participant CH as videoProcCh
    participant PL as videoProcessingLoop
    participant PC as pipelineCodecs
    participant PR as programRelay

    SD->>SW: handleRawVideoFrame(key, pf)
    activate SW
    Note over SW: s.mu.RLock()<br/>Sees inTransition=true<br/>s.mu.RUnlock()
    deactivate SW

    SW->>TE: IngestRawFrame(key, yuv, w, h, pts)
    activate TE
    Note over TE: e.mu.Lock()<br/>Store YUV, compute blend<br/>Call blendMix/blendWipe/etc.<br/>e.mu.Unlock()
    deactivate TE

    TE->>SW: Output(blended, w, h, pts, isKF)
    activate SW
    Note over SW: broadcastProcessed()<br/>s.mu.RLock() — read refs<br/>s.mu.RUnlock()<br/>Deep-copy YUV (pool)
    deactivate SW

    SW->>CH: enqueueVideoWork
    CH->>PL: work := <-videoProcCh
    PL->>PC: encode(pf, isKF)
    PL->>PR: BroadcastVideo(frame)
```

**Lock sequence:** `s.mu` RLock → `e.mu` Lock → release → `s.mu` RLock → release → `pc.mu` Lock → release

**Key insight:** Since sources are pre-decoded by sourceDecoder goroutines, the transition engine receives raw YUV and only needs to blend + output. No decode step in the engine lock.

---

### Flow 3: Audio Frame

Audio frames flow through the mixer, which has its own lock independent of the video path.

```mermaid
sequenceDiagram
    participant SV as sourceViewer
    participant SW as Switcher
    participant MX as AudioMixer
    participant ENC as AAC Encoder
    participant PR as programRelay

    SV->>SW: handleAudioFrame(key, frame)
    activate SW
    Note over SW: s.mu.RLock()<br/>Read: audioHandler<br/>s.mu.RUnlock()
    deactivate SW

    SW->>MX: IngestFrame(key, frame)
    activate MX
    Note over MX: m.mu.RLock() — delay check<br/>m.mu.RUnlock()

    Note over MX: m.mu.Lock()<br/>Decode → Trim → EQ →<br/>Compressor → Fader → Mix →<br/>Master → Limiter → Encode<br/>m.mu.Unlock()
    deactivate MX

    MX->>PR: BroadcastAudio(frame)
    Note over PR: After m.mu released
```

**Lock sequence:** `s.mu` RLock → release → `m.mu` RLock → release → `m.mu` Lock → release

**Note:** The full mix cycle (decode through encode) runs under `m.mu` Lock. This is acceptable because audio frames are small (~500 bytes AAC) and the cycle is fast (~0.5ms). The lock is released before broadcast.

---

### Flow 4: Cut Operation

A cut changes the program source. Since all sources are continuously decoded
(always-decode architecture), cuts are instant — no GOP replay or IDR gating needed.

```mermaid
sequenceDiagram
    participant API as HTTP Handler
    participant SW as Switcher
    participant MX as AudioMixer

    API->>SW: Cut(ctx, sourceKey)
    activate SW
    Note over SW: s.mu.Lock()<br/>Change programSource<br/>buildStateLocked()<br/>s.mu.Unlock()
    deactivate SW

    SW->>MX: OnCut(old, new)
    activate MX
    Note over MX: m.mu.Lock()<br/>Setup crossfade<br/>m.mu.Unlock()
    deactivate MX

    SW->>MX: OnProgramChange(new)
```

**Lock sequence:** `s.mu` Lock → release → `m.mu` Lock → release

**Key insight:** Each lock is acquired and released independently — no nesting. The Cut operation is sub-millisecond. Since all sources are continuously decoded (always-decode architecture), cuts are instant — no GOP replay or IDR gating needed.

---

### Flow 5: MXL Raw Video Frame

MXL sources bypass the Prism relay and deliver raw YUV420 directly.

```mermaid
sequenceDiagram
    participant MXL as MXL Reader
    participant SRC as MXL Source
    participant SW as Switcher
    participant TE as TransitionEngine
    participant CH as videoProcCh
    participant PC as pipelineCodecs
    participant PR as programRelay

    MXL->>SRC: videoFanOut(grain)
    Note over SRC: V210→YUV420 conversion<br/>(no locks, pre-allocated buffers)

    SRC->>SW: IngestRawVideo(key, yuv, w, h, pts)
    activate SW
    Note over SW: s.mu.RLock()<br/>Read: sources, state,<br/>transEngine, programSource<br/>s.mu.RUnlock()
    deactivate SW

    alt During transition
        SW->>TE: IngestRawFrame(key, yuv, w, h, pts)
        Note over TE: Same as Flow 2 Phase 1-3
    else Normal
        SW->>CH: enqueueVideoWork(yuvFrame)
        CH->>PC: encode(pf)
        PC->>PR: BroadcastVideo(frame)
    end
```

**Lock sequence:** `s.mu` RLock → release → (same as Flow 1 or 2 from here)

**Key insight:** MXL frames skip the GOP cache (no AVC1 data to cache) and skip the delay buffer / frame sync (raw YUV is already synchronized by MXL's shared memory clock).

---

### Flow 6: Output Path

From program relay through MPEG-TS muxing to SRT or file.

```mermaid
sequenceDiagram
    participant PR as programRelay
    participant OV as OutputViewer
    participant MUX as TSMuxer
    participant CB as Output Callback
    participant AA as AsyncAdapter
    participant SRT as SRT Caller

    PR->>OV: SendVideo(frame)
    Note over OV: Non-blocking channel send<br/>(drop if full, cap=100)

    OV->>MUX: WriteVideo(frame)
    activate MUX
    Note over MUX: m.mu.Lock()<br/>AVC1→AnnexB (reused buf)<br/>Mux to MPEG-TS<br/>Flush → output callback<br/>m.mu.Unlock()
    deactivate MUX

    MUX->>CB: output(tsData)
    Note over CB: adapters := atomic.Load()<br/>No lock needed

    CB->>AA: Write(tsData)
    Note over AA: sync.Pool get → copy<br/>Non-blocking channel send<br/>(drop if full, cap=256)

    AA->>SRT: inner.Write(data)
    Note over SRT: c.mu.Lock()<br/>conn.Write or ringBuf<br/>c.mu.Unlock()
```

**Lock sequence:** `mux.mu` Lock → release → (atomic load) → `srt.mu` Lock → release

**Key insight:** The `AsyncAdapter` decouples the muxer from slow SRT writes. The adapters list uses `atomic.Pointer` so the muxer callback never needs the OutputManager lock.

---

## Lock Ordering Rules

These rules prevent deadlocks. Every lock acquisition follows this hierarchy — a goroutine holding a lower-numbered lock never acquires a higher-numbered one.

```mermaid
graph TD
    A["1. Switcher s.mu"] --> B["2. TransitionEngine e.mu"]
    A --> D["3. pipelineCodecs pc.mu"]
    A --> E["4. AudioMixer m.mu"]
    A --> F["5. FrameSynchronizer fs.mu"]
    F --> G["6. syncSource ss.mu"]
    H["7. OutputManager m.mu"] --> I["8. OutputDestination dest.mu"]
    H --> J["9. TSMuxer mux.mu"]

    style A fill:#ff9999
    style F fill:#99ccff
    style H fill:#99ff99
```

### Rules

1. **Switcher (`s.mu`) is always released** before acquiring any other lock.
   - `handleRawVideoFrame`: RLock → release → then call transEngine, enqueue work, etc.
   - `Cut`: Lock → release → then call mixer.

2. **FrameSynchronizer uses two-level locking:** `fs.mu` (global) then `ss.mu` (per-source). Never reversed.

3. **OutputManager releases before viewer/muxer stop:** `stopMuxerLocked` releases `m.mu` before calling `viewer.Stop()` to avoid deadlock with the muxer output callback.

4. **No cross-subsystem lock nesting:** The video pipeline (Switcher → pipelineCodecs) and the audio pipeline (Switcher → Mixer) never hold each other's locks simultaneously.

5. **Transition engine releases before callbacks:** `IngestFrame` releases `e.mu` before calling `Output`, preventing the engine lock from blocking the switcher's broadcast path.

---

## Concurrency Patterns

### Pattern 1: Read-Copy-Update (RCU) Style

The switcher hot path reads state under RLock, copies values to locals, releases the lock, then processes without any lock:

```
handleVideoFrame:
    s.mu.RLock()
    programSource := s.programSource     ← copy to local
    state := s.state                     ← copy to local
    engine := s.transEngine              ← copy pointer
    ss := s.sources[sourceKey]           ← copy pointer
    s.mu.RUnlock()
    ... process using locals (no lock) ...
```

This means writes (Cut, SetPreview) only block briefly to update fields.

### Pattern 2: Lock-Free Fast Path

The delay buffer checks `hasAnyDelay.Load()` before locking. When no source has delay (the common case), the frame passes through with zero lock acquisitions:

```
handleVideoFrame:
    if !db.hasAnyDelay.Load() {          ← atomic, no lock
        db.handler.handleVideoFrame(...)  ← direct call
        return
    }
    db.mu.Lock()                          ← only when delay is active
    ...
```

### Pattern 3: Prepare Outside, Commit Under Lock

pipelineCodecs holds `pc.mu` only for config checks and state updates, not for the actual encode (5–30ms):

```
encode():
    pc.mu.Lock()                          ← brief: check encoder init
    encoder := pc.encoder
    pc.mu.Unlock()                        ← release before expensive work

    encoded, isKF := encoder.Encode()     ← no lock (expensive: 5-30ms)

    pc.mu.Lock()                          ← brief: state update
    pc.groupID = ...
    pc.mu.Unlock()
```

### Pattern 4: Atomic Pointer Swap

The OutputManager updates its adapter list under lock, but stores it in an `atomic.Pointer` so the muxer callback can read it lock-free:

```
rebuildAdaptersLocked:                    ← called under m.mu
    list := buildAdapterList()
    m.adapters.Store(&list)               ← atomic store

output callback:                          ← called from muxer (no m.mu)
    adapters := m.adapters.Load()         ← atomic load, no lock
    for _, a := range *adapters { ... }
```

### Pattern 5: Channel as Backpressure

The video processing channel (`videoProcCh`, cap=8) decouples frame ingestion from encoding. When the encoder falls behind, the newest-wins drop policy discards the oldest frame:

```
enqueueVideoWork:
    select {
    case s.videoProcCh <- work:           ← normal: enqueue
    default:
        <-s.videoProcCh                   ← drop oldest
        s.videoProcCh <- work             ← enqueue newest
    }
```

---

## Per-Frame Lock Budget

At 30 fps, each frame has a 33ms budget. Here's the lock overhead per frame:

| Lock | Acquisitions/frame | Hold time | Total |
|------|--------------------|-----------|-------|
| `s.mu` RLock | 2–3 | ~100ns each | ~300ns |
| `pc.mu` Lock | 2 (encode only) | ~100ns each | ~200ns |
| `mux.mu` Lock | 1 | ~500ns | ~500ns |
| **Total lock overhead** | | | **~1.0µs** |
| **Frame budget** | | | **33,000µs** |
| **Lock overhead %** | | | **0.004%** |

The actual expensive work (decode: ~2ms, blend: ~1ms, encode: ~5ms) runs entirely without locks.

---

## Deadlock-Free Guarantees

The system is deadlock-free because:

1. **No circular dependencies:** Lock ordering is a strict DAG (diagram above).
2. **No lock held during expensive operations:** Decode, blend, encode run outside all locks.
3. **No lock held across goroutine boundaries:** Every lock is acquired and released within the same function call (or deferred).
4. **Channels never block producers on the hot path:** All channel sends use `select` with `default` for non-blocking behavior.
5. **OutputManager releases lock before waiting:** `stopMuxerLocked` explicitly releases `m.mu` before `viewerWg.Wait()`.
