# SwitchFrame Architecture

## 1. System at a Glance

SwitchFrame is a server-authoritative live video switcher: all switching, mixing, compositing, and encoding happen on the server. Browsers connect over WebTransport as thin control surfaces -- they display source previews and send operator commands, but the server produces the definitive program output. Sources arrive via Prism MoQ ingest (H.264/AAC cameras over the internet) or MXL shared-memory transport (uncompressed V210 from local infrastructure).

```mermaid
flowchart LR
    subgraph ingest ["Source Ingest"]
        moq["MoQ Sources<br/>(H.264 / AAC)"]
        mxl["MXL Sources<br/>(V210 shared mem)"]

        moq --> relay["Per-Source<br/>Prism Relay"]
        relay --> sv["sourceViewer"]
        sv --> sd["sourceDecoder<br/>H.264 → YUV420"]

        mxl --> v210["V210 → YUV420"]

        sd --> yuv["Raw YUV420"]
        v210 --> yuv
    end

    subgraph switching ["Switching Engine"]
        fsync["Frame Sync<br/>(align mixed rates)"]
        delay["Delay Buffer<br/>(lip-sync)"]
        core["Switcher Core<br/>(cut / preview /<br/>frame routing)"]
        trans["Transition Engine<br/>(mix, dip, wipe,<br/>stinger, FTB)"]

        fsync --> delay --> core
        core --> trans
    end

    subgraph vidpipe ["Video Pipeline"]
        direction LR
        usk["Upstream<br/>Key"] --> pip["PIP /<br/>Layout"]
        pip --> dsk["DSK<br/>Graphics"]
        dsk --> raw["Raw Sink"]
        raw --> enc["H.264<br/>Encode"]
    end

    subgraph audpipe ["Audio Pipeline"]
        direction LR
        adec["AAC<br/>Decode"] --> trim["Trim"]
        trim --> eq["EQ"]
        eq --> comp["Compressor"]
        comp --> fader["Fader"]
        fader --> mix["Mix"]
        mix --> master["Master"]
        master --> lim["Limiter"]
        lim --> aenc["AAC<br/>Encode"]
    end

    subgraph output ["Output"]
        prog["Program Relay"]
        browsers["Browsers<br/>(WebTransport / MoQ)"]
        rec["Recording<br/>(MPEG-TS)"]
        srt["SRT<br/>Destinations"]
        mxlout["MXL Output<br/>(shared mem)"]

        prog --> browsers
        prog --> rec
        prog --> srt
        prog --> mxlout
    end

    subgraph control ["Control Plane"]
        rest["REST API<br/>(HTTP/3)"]
        mqctl["MoQ Control Track<br/>(state broadcast)"]
    end

    yuv --> fsync
    trans --> usk
    enc --> prog
    aenc --> prog

    rest -.->|"commands"| core
    mqctl -.->|"state updates"| browsers
```

The key architectural insight is that every source is continuously decoded to raw YUV420, regardless of how it arrives. All video processing -- transitions, upstream keying, PIP compositing, graphics overlay, scaling -- operates in BT.709 YUV420, the same colorspace hardware broadcast mixers use internally. This eliminates costly YUV-to-RGB round-trips and means cuts between sources are instant: there is no keyframe wait because every source always has a current decoded frame ready.

Audio follows a similar always-ready model. Each channel flows through a fixed processing chain before being mixed to a stereo master bus. A passthrough optimization bypasses the entire decode/process/encode chain when a single source is at unity gain with no processing enabled, dropping audio CPU to near zero in the common case.

## 2. A Frame's Journey

Following a single frame from camera to screen reveals how the pieces fit together. The path differs slightly for MoQ (H.264) and MXL (uncompressed V210) sources, but both converge on the same raw YUV420 processing pipeline.

### MoQ Source Path

```mermaid
sequenceDiagram
    participant Camera
    participant Relay as Prism Relay
    participant SV as sourceViewer
    participant SD as sourceDecoder
    participant Sync as FrameSync / Delay
    participant SW as Switcher
    participant Pipe as Pipeline
    participant PR as Program Relay
    participant Browser

    Camera->>Relay: H.264 frame (MoQ publish)
    Relay->>SV: SendVideo()
    SV->>SD: dispatch via atomic.Pointer
    SD->>SD: H.264 → YUV420 (FFmpeg multi-threaded)
    SD->>Sync: decoded frame
    Sync->>SW: handleRawVideoFrame()
    Note over SW: record health · update stats · feed key bridge
    SW->>Pipe: enqueue via buffered channel
    Note over Pipe: upstream key → PIP → DSK → raw sink → encode
    Pipe->>PR: BroadcastVideo (H.264)
    PR->>Browser: MoQ subscribe
    Note over Browser: WebCodecs decode → Canvas render
```

### MXL Source Path

```mermaid
flowchart TD
    MXL["MXL Shared Memory"] --> Reader["Reader goroutines"]

    Reader --> YUV["Raw YUV420 → Switcher<br/>(IngestRawVideo)"]
    Reader --> PCM["Float32 PCM → Audio Mixer<br/>(IngestPCM, bypasses AAC decode)"]
    Reader --> Encode["Encode H.264/AAC → Browser relay"]
    Reader -.-> Data["Data grains → SCTE-104 translator"]
```

MXL sources bypass the sourceViewer and sourceDecoder entirely -- the V210-to-YUV420 conversion happens in the reader goroutine, and raw frames are injected directly into the switcher. Audio arrives as float32 PCM and skips AAC decoding. A third fan-out encodes to H.264/AAC for browser preview, since browsers cannot consume raw YUV over MoQ.

### Always-Decode Architecture

Every source gets a dedicated decoder goroutine that continuously produces YUV420 frames. This is the key enabling decision -- it eliminates GOP caches, pending-IDR flags, and keyframe gating. When the operator cuts to a new source, the next decoded frame flows through immediately. The tradeoff is CPU cost (N decoders always running), offset by FFmpeg's multi-threaded software decode.

### Frame Memory Management

YUV420 buffers are managed by a FramePool -- a mutex-guarded LIFO free list of pre-allocated buffers. This achieves >99% hit rate vs ~19% with Go's sync.Pool, because LIFO ordering keeps hot buffers in L1/L2 cache. Frames are returned to the pool after encode, and the pool pre-allocates 32 buffers at the pipeline resolution.

### Pipeline Architecture

The video pipeline is a chain of immutable processing nodes, built once and atomically swapped for reconfiguration. When something changes (compositor on/off, upstream key added, graphics layer toggled), a new pipeline is built and swapped in via atomic pointer. The old pipeline drains in-flight frames in a background goroutine. Zero frames are dropped during reconfiguration.

### Timing

The hot path holds locks for under 1us per frame. The async handoff between the switcher and pipeline uses an 8-frame buffered channel (~267ms at 30fps), decoupling source delivery jitter from encode latency. Always-on re-encode ensures consistent SPS/PPS across transition boundaries, so downstream decoders never need reconfiguration.

## 3. A Cut Happens

A cut is the simplest and most frequent operation: swap the program source instantly with no transition frames. Because every source is continuously decoded by its own goroutine, there is no keyframe wait -- the new source always has a current YUV420 frame ready.

```mermaid
sequenceDiagram
    participant Operator
    participant Browser
    participant Server
    participant Switcher
    participant Mixer
    participant SP as StatePublisher
    participant All as All Browsers

    Operator->>Browser: press Space / click CUT
    Browser->>Browser: optimistic update (instant UI response)
    Browser->>Server: POST /api/cut {source: "cam2"} (HTTP/3)
    Server->>Switcher: Cut("cam2")
    Note over Switcher: write lock: swap program ↔ preview,<br/>increment sequence number
    Switcher->>Mixer: OnCut(old, new)
    Note over Mixer: equal-power crossfade (cos/sin), ~23ms
    Switcher->>Mixer: OnProgramChange(new)
    Note over Mixer: AFV: activate new source channel,<br/>deactivate others
    Switcher->>SP: broadcast state change
    SP->>SP: enrich (merge recording, SRT,<br/>graphics, replay, operator status)
    SP->>All: MoQ control track (full JSON snapshot)
    Note over All: reconcile optimistic update<br/>with server confirmation
```

### Switcher State Machine

The switcher has a small state machine governing what operations are valid at any moment. A cut bypasses the transitioning state entirely -- it is a direct program/preview swap within the idle state.

```mermaid
stateDiagram-v2
    [*] --> Idle
    Idle --> Transitioning : StartTransition()
    Transitioning --> Idle : complete / abort
    Idle --> FTBActive : FadeToBlack()
    FTBActive --> FTBReversing : FadeToBlack() (toggle)
    FTBReversing --> Idle : complete
```

### Why Cuts Are Instant

The always-decode architecture is what makes cuts zero-latency. Every source has a dedicated `sourceDecoder` goroutine continuously producing YUV420 frames, so the new source already has a decoded frame in its ring buffer. On the next tick after `Cut()`, cam2's decoded frame flows through `handleRawVideoFrame` into the pipeline node chain and out to encode. There is no GOP replay, no IDR gating, no decoder warmup.

The audio mixer applies a one-frame (~23ms) equal-power crossfade between the old and new source to prevent audible clicks. The crossfade uses precomputed cos/sin lookup tables (1024 entries) to avoid per-sample `math.Cos` calls. Channels in AFV mode automatically activate or deactivate to match the new program source.

The browser applies the cut optimistically before the server confirms -- the UI swaps tally colors and source labels immediately on keypress. If the server rejects the cut (e.g., source offline, operator lacks permission), the pending action expires after 2 seconds and reverts to server state. In practice, the server confirms within a few milliseconds over the shared QUIC connection, and the MoQ control track update arrives before the timeout is relevant.
