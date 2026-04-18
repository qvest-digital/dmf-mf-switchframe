# State broadcast

> **Where you are:** [docs](../README.md) → reference → state-broadcast
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [reference/api.md](api.md) · [subsystems/control-plane.md](../subsystems/control-plane.md) · [fast-control.md](fast-control.md)

**TL;DR** `ControlRoomState` is a single, monolithic JSON blob that captures every piece of switcher state the UI cares about — program/preview sources, transition, every audio channel, tally, recording and SRT output, destinations, graphics and DVE layers, replay, clips, operators and locks, captions, comms, ST map, color grade, playout, SCTE-35, peer status, and command-sequence bookkeeping. It is broadcast on every state change (and in response to most REST mutations) over a MoQ `control` track; the UI also pulls it via `GET /api/switch/state` as a fallback. This doc describes the full schema, delivery model, and emission triggers.

## The story

Switchframe's control plane has a single source of truth for shared state: the `ControlRoomState` struct defined in [`server/internal/types.go`](../../server/internal/types.go). The browser never reconstructs state from individual REST responses alone — it reconciles off the broadcast. This keeps multi-operator sessions, multi-engine redundancy, and the dozens of REST endpoints all consistent: whoever changes state wins, whoever reads state always sees the same snapshot.

Broadcasts happen through two conduits. The primary conduit is a MoQ **control track** — a push channel that buffers the latest serialized JSON and ships it to connected subscribers. The secondary conduit is REST polling on `GET /api/switch/state`, used for initial load and when the browser's MoQ stream is unavailable. Both paths yield the same JSON object.

Every REST handler that changes state calls `(*API).broadcast()` after its change lands. Background subsystems (switcher, mixer, compositor, replay, output, macro runner, playout manager, session manager) register `OnStateChange` callbacks on the API which likewise invoke `broadcast()`. This means mid-transition frame updates, per-frame audio peaks, running macro step transitions, and SRT reconnect events all land as fresh broadcasts without the client explicitly polling.

## How it works

```mermaid
flowchart LR
    subgraph Writers
        H[REST handlers] --> B[(*API).broadcast]
        Mxc[Mixer tick] --> B
        Sw[Switcher transition] --> B
        Cp[Compositor] --> B
        Rp[Replay manager] --> B
        Op[Session manager] --> B
        Mc[Macro runner] --> B
        Pl[Playout manager] --> B
    end

    B --> Ef[enrichFunc - adds output, graphics, replay slices]
    Ef --> Pub[StatePublisher / ChannelPublisher]
    Pub --> JSON[json.Marshal]
    JSON --> MoQ[MoQ control track]
    MoQ --> UI[Browser control-room.svelte.ts]

    HS[GET /api/switch/state] --> Ef
    UI -.polling fallback.-> HS
```

The writer side is fan-in: every path that changes state calls `(*API).broadcast()`. That method loads an atomic pointer to a `broadcastFunc` registered by the app layer at startup. The broadcast function calls `(*API).enrichedState()` to fetch the current switcher state and walk it through the registered `enrichFunc` — which stitches in output status, graphics state, operator list, replay state, playout state, and all other subsystems that live outside the core switcher. The enriched state is then handed to a publisher.

Two publishers ship: `StatePublisher` calls a user-supplied callback synchronously (used in tests and simple deployments); `ChannelPublisher` pushes serialized JSON onto a buffered channel that is drained by the MoQ broadcaster goroutine. The channel drops oldest messages when full (with `dropped` counter for observability) so a slow reader cannot back-pressure the control plane.

## Schema

The full schema is defined in [`server/internal/types.go`](../../server/internal/types.go) under `ControlRoomState`. Every field is `json`-tagged and uses `omitempty` where appropriate to keep the wire small.

### Core switching

| Field | Type | Meaning |
|---|---|---|
| `programSource` | string | current program bus source key |
| `previewSource` | string | current preview bus source key |
| `transitionType` | string | `""`, `cut`, `mix`, `dip`, `wipe`, `stinger`, or any `dve-*` preset ID |
| `transitionDurationMs` | int | duration of the current/last transition |
| `transitionPosition` | float | 0.0-1.0 T-bar position (non-zero during transition) |
| `transitionEasing` | string | easing type used |
| `inTransition` | bool | true while a transition is mid-flight |
| `ftbActive` | bool | true when Fade-to-Black is engaged |
| `tallyState` | `map[string]string` | per-source tally: `red` (program), `green` (preview), `amber` (FTB) |
| `pipelineFormat` | `PipelineFormatInfo?` | current resolution / fps / codec |
| `encoder` | `EncoderState?` | active encoder + list of available alternatives |

### `AudioChannel`

Keyed by source under `audioChannels`. See [`server/internal/types.go`](../../server/internal/types.go) for all fields; summary:

- Fader: `level`, `trim`, `muted`, `afv`, `phaseInvert`, `balance`
- Metering: `peakL`, `peakR`, `gainReduction`, `gateReduction`, `audioDelayMs`
- Per-channel DSP: `eq` (3-band), `compressor`, `gate`, `gateEnabled`, `hpfFrequency`, `hpfEnabled`

Plus master-bus fields at the top level: `masterLevel`, `programPeak`, `gainReduction`, `limiter`, `momentaryLufs`, `shortTermLufs`, `integratedLufs`.

### `SourceInfo`

Keyed by source key under `sources`. One entry per registered source:

- Identity: `key`, `label`, `type` (`demo`/`mxl`/`srt`/`replay`/`clip`), `status`, `position`
- Processing: `delayMs`, `keyConfig` (upstream key — `SourceKeyConfig`), `corrector` (per-source color corrector)
- Metadata: `srt` (live SRT stats when applicable — `SRTSourceInfo`), `isVirtual`, `hasCaptions`

### Output

- `recording` — `RecordingStatus` (active, filename, bytes, duration, dropped, error)
- `srtOutput` — `SRTOutputStatus` (legacy single-stream SRT)
- `destinations` — `[]DestinationInfo` (multi-destination output: SRT caller / SRT listener / RTMP)
- `cbr` — `CBRStatus` (null-packet pacer for CBR SRT)

### Graphics and composition

- `graphics` — `GraphicsState` — DSK layer list with rect, z-order, image name, HTML5 status, sequence state, fade position, animation mode
- `layout` — `LayoutState` — PIP / DVE slots with full transform state, plus DVE preset list and animation flag
- `stmap` — `STMapState` — per-source and program map assignments plus available maps

### Replay and clips

- `replay` — `ReplayState` — current play state, speed, loop, position, marks, per-source buffer info, GPU-accel flag, shutter angle
- `clipPlayers` — `[]ClipPlayerInfo` — four player slots
- `clipCount` — total clips in library
- `clipUpload` — `ClipUploadProgress` — stage/percent/filename while an upload is in progress

### Playout

- `playout` — `PlayoutState` — one `PlayoutChannelInfo` per channel with state, mode, playlist name, current/next item, item progress, active pod, pod list, pod counters

### Captions and comms

- `captions` — `CaptionState` — mode, author-mode buffer, per-source caption availability
- `comms` — `CommsState` — active flag, participant list with speaking/muted flags

### Effects

- `colorGrade` — `ColorGradeState` — current look name, intensity, available looks, corrector state
- `aiSegmentation` — `AISegmentationState` — availability, loaded model name, per-source configs
- `neuralKey` — `NeuralKeyState` — availability flag (`{ "available": false }` on non-GPU builds)
- `asr` — `ASRState` — availability, active, backend, model, opaque backend config, confidence/speaker presets

### Macros

- `macro` — `MacroExecutionState` — running flag, macro name, per-step status (`pending/running/done/failed/skipped`), wait-start timestamps

### Presets

- `presets` — `[]PresetInfo` — ID + name summaries (full preset data fetched via `/api/presets/{id}`)

### Operators

- `operators` — `[]OperatorInfo` — registered operators with `connected` flag
- `locks` — `map[string]LockInfo` — per-subsystem lock status

### Dual-engine

- `peerReachable` — `*bool` — nil when no peer configured
- `peerLastSeenMs` — Unix ms of last successful peer health probe
- `engineLabel` — `""` (standalone) / `"a"` / `"b"`
- `role` — `standalone` / `leader` / `follower-healthy` / `follower-partitioned`
- `engineBUrl` — origin URL for peer engine (used by browser clock sync)
- `sessionId` — SaaS session ID (empty standalone)
- `seq` — monotonic broadcast sequence
- `lastCommandSeq` — highest enqueued command sequence
- `lastExecutedSeq` — highest successfully executed command
- `lastExecutedErr` — last execution error
- `lateCommands` — count of commands that arrived after their scheduled time
- `recentCommandErrors` — `[]CommandError` — ring buffer of failed executions
- `lastChangedBy` — name of operator who made the most recent change (badge in UI)
- `timestamp` — Unix ms of broadcast

### SCTE-35

- `scte35` — `SCTE35State` — enabled flag, SCTE-104 flag, `activeEvents` map, `eventLog` ring, heartbeat OK, injector config

### Connection metadata

- `connectionInfo` — `ConnectionInfo` — domain, SRT ingest port, SRT output ports (for UI to build streamurl strings)

## Push cadence and triggers

Broadcasts fire on:

- **REST handler completion** — every state-changing endpoint calls `broadcast()` before returning. Read-only endpoints don't broadcast.
- **Switcher internal state change** — `switcher.OnStateChange` fires on any switcher mutation, including the per-frame `TransitionPosition` updates during a transition.
- **Mixer tick** — the mixer publishes a state update every ~21 ms with fresh peaks and gain reductions. (Not every change is a full broadcast; the mixer coalesces.)
- **Compositor updates** — layer activation, fade completion, animation tick.
- **Session manager** — lock acquire/release and operator connect/disconnect, plus the 15-second stale-session cleanup when it removes sessions.
- **Macro runner** — every step transition (pending→running→done/failed).
- **Playout manager** — channel state transitions, pod lifecycle.
- **Clip upload** — progress updates during upload/analyze/transcode/validate stages.

The `ChannelPublisher` drops the oldest message when the 256-element channel buffer fills, so a slow subscriber never back-pressures broadcasters. `DroppedCount()` is available for observability.

## Delivery

The serialized JSON is published onto a MoQ track named `control` in the same WebTransport session as the preview and program tracks. The UI's `control-room.svelte.ts` subscribes to the track and calls `JSON.parse` on each received object, merging it into the Svelte 5 `$state` store. The full state is shipped every time — there is no delta or patch model. This is intentional: state is small (~10-30 KB typical), JSON parsing is fast, and the simplicity dramatically reduces synchronization bugs.

When the MoQ subscription hasn't attached (early in startup, or on reconnection), the browser falls back to polling `GET /api/switch/state` at 1 Hz. Once MoQ comes up, polling stops.

## JSON serialization details

- Marshaling uses the stdlib `encoding/json` with struct tags.
- `omitempty` is applied to most pointer-typed and map-typed fields so a bare engine (no operators configured, no SCTE-35, etc.) emits a lean JSON.
- Maps with `null` values (e.g., empty `audioChannels`) come through as `{}` after normalization in handlers. `TallyState` is always present (never nil) so the UI can unconditionally read it.
- Timestamps are Unix milliseconds (int64) for operator timestamps (`locks[...].acquiredAt`), Unix microseconds for fast-control pong (`lastPongSentUs`), or 90 kHz ticks for SCTE-35 splice times (`spliceTimePts`). The doc body on each endpoint notes the unit where ambiguous.

## Gotchas and invariants

- **Never mutate in-place on the reader.** The UI reconciles by replacing state, not patching.
- **Broadcast is fire-and-forget.** If a broadcast is dropped (channel full), the next one will include the latest state. This is safe because state is idempotent.
- **Command-sequence fields (`seq`, `lastCommandSeq`, `lastExecutedSeq`) are the only piece of causality ordering.** They advance monotonically and are used by the UI to decide whether a pending local change has been acknowledged by the server.
- **`timestamp` is set by the server at publish time**, so clients can display "X seconds ago" and detect when broadcasts stall.
- **`enrichFunc` is registered once at startup.** Handlers that stitch in subsystem state do so through this function, not by mutating the state directly. See [subsystems/control-plane.md](../subsystems/control-plane.md).

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md) · [locking-and-concurrency.md](../concepts/locking-and-concurrency.md)
- **Reference:** [api.md](api.md) · [fast-control.md](fast-control.md)
- **Subsystems:** [control-plane.md](../subsystems/control-plane.md) · [switcher.md](../subsystems/switcher.md)
- **Integration:** [ui-server-contract.md](../integration/ui-server-contract.md)
