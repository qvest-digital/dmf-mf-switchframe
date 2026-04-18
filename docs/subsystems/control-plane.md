# Control plane

> **Where you are:** [docs](../README.md) → subsystems → control-plane
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [reference/api.md](../reference/api.md) · [reference/state-broadcast.md](../reference/state-broadcast.md) · [subsystems/switcher.md](switcher.md)

**TL;DR** The control plane is the Go package layer that mediates between external clients (the browser UI, automation webhooks, operator sessions) and the live switcher/mixer/subsystems. It lives in `server/control/`, `server/operator/`, `server/preset/`, and `server/macro/`. Its jobs: route REST requests, authenticate callers, gate by role/lock/peer status, dispatch to subsystems, broadcast state, run macros, save and recall presets, and keep the per-operator session + subsystem-lock state straight. This doc walks the whole vertical end to end.

## The story

Every external request — UI button, automation webhook, `curl` from an operator's phone — enters the control plane. The plane is built out of four tightly coupled packages:

- **`server/control/`** — the HTTP mux, middleware stack, handler functions, per-subsystem adapter interfaces, error-code mapping, and the state publisher. Around 40 `.go` files split into `api_*.go` per feature and a small set of shared pieces (`auth.go`, `middleware.go`, `errmap.go`, `state.go`, `timed.go`, `validate.go`).
- **`server/operator/`** — persistent operator records, per-operator bearer tokens, live session tracking with 60-second staleness, subsystem locks with role-based permissions, and the HTTP middleware that enforces lock ownership.
- **`server/preset/`** — switcher preset storage (file-based JSON) and a best-effort recall that applies as much of a preset as possible while collecting warnings for the rest.
- **`server/macro/`** — macro CRUD (again file-based JSON), a sequential runner, per-step validation, and progress broadcasting.

Because the REST API is the single entry point for every state change — the browser UI, automations, webhook receivers, monitoring probes — the control plane holds the most concentrated set of invariants in the whole codebase. It must:

1. Reject unauthenticated traffic.
2. Route to the right handler, with method-aware path templates.
3. Validate inputs before they touch core subsystems.
4. Enforce role permissions + subsystem locks on state-mutating requests.
5. Gate outputs (SRT / recording) on peer follower status to prevent dual-feed in a redundancy deployment.
6. Sequence commands so dual-engine deployments can execute in lock-step.
7. Return a consistent response shape and map known errors to HTTP status codes.
8. Broadcast the resulting state to every connected client.
9. Write metrics and logs with enough structure to debug support cases.

The flow of a single request touches every one of these.

## How it works

```mermaid
flowchart TB
    Req[HTTP request] --> CORS[CORS middleware]
    CORS --> MB[MaxBytes middleware]
    MB --> LOG[Logger middleware]
    LOG --> METR[Metrics middleware]
    METR --> AUTH[Auth middleware - session token or operator token]
    AUTH --> OP[Operator middleware - role and lock check]
    OP --> MUX[http.ServeMux - route to api_*.go handler]
    MUX --> TIMED[timedHandler - sequence + pre-read body]
    TIMED --> VAL[handler: validate args]
    VAL --> SUB[dispatch to switcher / mixer / compositor / etc.]
    SUB --> BC[(*API).broadcast - publish ControlRoomState]
    SUB --> RESP[json.NewEncoder.Encode - enriched state]
    RESP --> Client[JSON 200 / 201 / 202 / 204]
```

### Middleware stack

Middleware is assembled by the app layer in `server/cmd/switchframe/app.go` and wraps the API mux before it's mounted on the main HTTP/3 server. Order matters:

1. **CORS** — `control.CORSMiddleware` adds permissive CORS headers for the dev UI running on a different origin. Defined in `control/cors.go`.
2. **MaxBytes** — `control.MaxBytesMiddleware` caps non-multipart JSON bodies at 16 MB before they're read. Multipart uploads (graphics images, clips, stingers) are exempt and use per-handler limits.
3. **Logger** — `control.LoggerMiddleware` generates or preserves `X-Request-ID`, attaches a child slog logger to the context, and logs the completed request with status, latency, and bytes written. Noisy polled paths (`/api/switch/state`, `/metrics`) log at `DEBUG`.
4. **Metrics** — `control.MetricsMiddleware` records `metrics.HTTPRequestsTotal` and `metrics.HTTPRequestDuration` labeled by method, pattern, and status. `pattern` is the bound Go 1.22 mux pattern (`"GET /api/sources/{key}"`), not the actual path — so path parameters don't blow out cardinality.
5. **Auth** — `control.AuthMiddleware` checks the `Authorization: Bearer <token>` header. Accepts the session API token first (timing-safe compare against the 32-byte startup token) and then delegates to an optional `OperatorTokenChecker` that wraps `operator.Store.GetByToken`. Returns 401 otherwise, with `WWW-Authenticate: Bearer realm="switchframe"` so browsers prompt correctly.
6. **Operator permission middleware** — `operator.NewMiddleware` (only attached when an operator store and session manager are both configured) maps the request path to a subsystem via `operator.EndpointSubsystem`, identifies the operator by bearer token, and rejects with 403 (role can't command) or 409 (subsystem locked by another operator). GETs and `/api/operator/*` paths bypass this middleware entirely.

A few paths are `authExemptPaths` and skip auth — certificate fingerprint, health, metrics, operator register/reconnect/heartbeat, peer-health, peer-force-leader (which uses a separate control-plane shared-secret header).

In demo mode the CLI installs `control.NoopAuthMiddleware` so the browser can hit the API without pre-flight token handshake.

### The `API` struct

All handlers hang off `control.API` defined in [`control/api.go`](../../server/control/api.go). It holds:

- A `*switcher.Switcher` (always present).
- Optional subsystem references set via `With*` options at construction: mixer, output manager, debug collector, perf sampler, preset store, graphics compositor, stinger store, macro store, keyer + bridge, replay manager, clip manager/store, playout manager/cache/prefetcher, operator store + session manager, SCTE-35 injector + rules, DVE compositor + preset store, wipe store, caption manager, text-animation / ticker / sequence engines, SRT manager, comms manager, AI segmenter, ASR manager, ST map registry + store, color-grade manager, per-source corrector manager, HTML5 graphics manager, peer poller.
- State bookkeeping: atomic pointer to the enrich function, last operator, upload progress, macro execution state, broadcast function, reconcile-lock state.
- The `*http.ServeMux` with all routes registered.

Interfaces like `AudioMixerAPI`, `OutputManagerAPI`, `SCTE35API`, `CommsManagerAPI`, `ASRManager`, `AISegmentManager`, `ColorGradeManager`, `SourceCorrectorManager`, `SRTManager`, `HTMLGfxManager`, `PerfAPI`, `DebugAPI`, `CaptionManagerAPI` are defined on the API struct to let each subsystem be swapped / stubbed / tested in isolation.

`NewAPI(sw, opts...)` applies the options and calls `registerRoutes()` which in turn calls `RegisterOnMux` — registering under both `/api/` and `/api/v1/` prefixes (the v1 alias is served by re-dispatching a cloned request with a rewritten path).

### Route registration

Each `api_<feature>.go` file has a `register<Feature>Routes(mux *http.ServeMux)` method that only registers its routes if its dependency is present. E.g.:

- `registerPresetRoutes` is a no-op when no preset store is configured.
- `registerSCTE35Routes` registers cue handlers only if a `SCTE35API` is configured, and rules handlers only if a `SCTE35RulesAPI` is configured.
- `registerReplayRoutes` is no-op without a replay manager.

This lets the binary ship stubs for features that require cgo or GPU (ASR, AI segmentation, neural key) without breaking the API surface.

### Timed command queue

A subset of state-mutating routes wrap their handlers with `timedHandler` or `timedHandlerWithPath` (defined in `control/timed.go`). These are core-switching routes that need to be executed deterministically across dual engines:

- Cut, preview, transition, transition-position, FTB, format set, encoder set.
- Source label, source delay, source position.
- Audio: trim, level, mute, AFV, phase, balance, master, EQ, compressor, HPF, gate, audio-delay, limiter.
- Graphics on/off, graphics fly-on.
- Upstream key set/delete.
- DVE slot on/off/update, layout set, preset recall.

The wrapper:

1. Reads the request body once into a buffer so both the queue and the handler can consume it.
2. Parses an optional `X-Scheduled-Us` header (Unix microseconds) — when the control plane schedules a command to run on a shared clock, this header tells the engine when.
3. Calls into `cmdqueue.Queue.Enqueue` (if configured) with a command payload containing the handler + body.
4. Bumps the command sequence and notifies the `seqTracker` callback so `ControlRoomState.LastCommandSeq` advances in the broadcast.
5. Waits for execution and writes the response.

On a single-engine deployment without a command queue, the wrapper degrades to an inline invocation — the sequence still advances but the command runs immediately.

### Error mapping

`control.errorStatus(err)` in [`control/errmap.go`](../../server/control/errmap.go) walks a fixed table of sentinel errors via `errors.Is()` and returns the matching HTTP status. The table is comprehensive (~90 sentinel errors) across switcher, audio, preset, stinger, macro, graphics, operator, replay, clip, SCTE-35, ST map, output, playout, comms, SRT. Unknown errors return 500.

`control/httperr/` packages up JSON error responses: `httperr.Write(w, status, msg)` and `httperr.WriteErr(w, status, err)`. Every handler uses these — never `http.Error` — so error bodies are always `application/json` shaped as `{"error": "..."}`.

### State broadcast

See [reference/state-broadcast.md](../reference/state-broadcast.md) for the full delivery model. Briefly:

- `(*API).broadcast()` loads an atomic-pointer `broadcastFunc` set at startup and invokes it.
- The broadcast function calls `(*API).enrichedState()` which runs `(*switcher.Switcher).State()` through the registered enrich function (set by the app layer) to stitch in output status, graphics state, operator info, replay state, etc.
- The enriched state is then serialized to JSON and pushed onto the MoQ control track through a `ChannelPublisher`.
- Handlers that mutate state call `broadcast()` directly. Subsystems also register their own `OnStateChange` callbacks which call `broadcast()` so mid-operation updates (per-frame transition position, compressor gain reduction, macro step transitions, playout channel state, session manager stale cleanup) land without explicit plumbing in each handler.

## Operators, sessions, and locks

Operator management is a deeper vertical than it looks because of how it composes with auth and locks.

### Roles and permissions

`operator.Role` defines five roles: `director`, `audio`, `graphics`, `captioner`, `viewer`. Each role has a permission matrix (`operator.rolePermissions`) mapping subsystems (`switching`, `audio`, `graphics`, `replay`, `output`, `captions`) to a bool. Directors can command everything; audio/graphics/captioner can command only their subsystem; viewer can command nothing (GETs always pass through).

Each role also has a lock matrix (`operator.roleLockable`) defining which subsystems that role can lock. Lockable == commandable, today.

### Registration and bearer tokens

`POST /api/operator/register` (unauthenticated) creates an operator with a generated UUID-like ID (`generateID`: 16 random bytes hex-encoded) and a bearer token (`generateToken`: 32 random bytes hex-encoded). When invite tokens are configured, the request body's `inviteToken` field overrides the role based on the token→role map. Session owners (requests authenticated with the session API token) bypass this check.

The operator record is persisted to `operators.json` atomically via a temp file + rename in `operator.Store.writeFile`, with rollback on failure so a save that fails mid-flight doesn't leave the in-memory slice inconsistent with the disk file.

Per-operator bearer tokens are accepted by `control.AuthMiddleware` alongside the session API token. This means automation scripts can authenticate as a named operator, and every request they make records who made the change (`ControlRoomState.LastChangedBy`).

### Sessions

`operator.SessionManager` tracks live sessions — who's connected, when they were last seen, and what locks they hold. Three mechanisms keep this fresh:

1. Operators heartbeat via `POST /api/operator/heartbeat` (roughly every 10 seconds from the UI). This bumps `LastSeen`.
2. A background cleanup goroutine (`cleanupLoop`) runs every 15 seconds and removes any session whose `LastSeen` is older than 60 seconds, also releasing any locks that session held.
3. Explicit `Connect` on registration/reconnect and `Disconnect` on operator delete.

The session manager uses a single mutex for everything — lock acquisition, session registration, stale cleanup — to avoid multi-mutex deadlocks. Callbacks (`OnStateChange`) are invoked outside the lock to keep the critical section small.

### Subsystem locks

An operator can lock a subsystem they have permission to command. Lock semantics:

- **Re-lock is idempotent.** The same operator re-locking the same subsystem returns success without emitting a state change.
- **Different operator → 409.** The REST handler returns `operator.ErrSubsystemLocked` which maps to HTTP 409.
- **Force-release requires director role.** Non-directors get 403.
- **Disconnect releases.** When an operator disconnects (explicit or stale), all their locks auto-release and state broadcasts fire.

The middleware checks locks on every state-mutating request against a bearer-token-identified operator. If the operator matches the lock holder, the request passes; otherwise 409. If no lock is held on the mapped subsystem, the request passes.

`operator.EndpointSubsystem` maps HTTP paths to subsystems via two tables: exact matches (`/api/switch/cut` → switching) and prefix matches (`/api/audio/*` → audio). The `/api/v1/` prefix is normalized back to `/api/` before the lookup so versioned routes hit the same rules.

## Presets

Presets capture a subset of switcher state — program/preview sources, transition type/duration, audio channel settings (level, muted, AFV), and master level. Graphics, DVE, SCTE-35, operator state, and playout are *not* captured.

### Storage

`preset.Store` (in [`server/preset/store.go`](../../server/preset/store.go)) holds an in-memory slice of `Preset` values and persists to a single JSON file via atomic temp-file + rename, with rollback on save failures. CRUD operations:

- `Create(name, snapshot)` — returns the new preset with a generated UUID and timestamp.
- `Update(id, updates)` — only name is mutable.
- `Delete(id)` / `DeleteByName(name)` — dual-engine setups need `DeleteByName` because each engine generates different IDs.
- `GetByName(name)` — same reasoning for `recallByName`.

Snapshots are created by `stateToSnapshot` in `control/api_preset.go`, which projects a `ControlRoomState` into a `ControlRoomSnapshot`.

### Recall

`preset.Recall(ctx, preset, target)` applies a preset to a `RecallTarget` — an interface implemented by `apiRecallTarget` that wraps the switcher and mixer. Recall is **best-effort**:

```
1. Cut the program source.
2. Set the preview source.
3. For each audio channel:
     a. SetLevel
     b. SetMuted
     c. SetAFV
4. SetMasterLevel.
```

Each step that fails emits a warning string (`audio channel "cam-gone" level: source not found`) and continues. The HTTP response returns both the preset and the warnings array so the UI can surface them.

## Macros

Macros automate sequences of switcher operations.

### Macro shape

A macro is a `{name, steps}` pair. Each step is `{action, params}` where `action` is one of ~60 values in `macro.allActions` — cut, preview, transition, wait, audio controls, graphics on/off/animate/fly/slide/text/ticker/sequence, recording, preset recall, key set/delete, source label/delay/position, replay mark-in/out/play, SCTE-35 cue/return/cancel/hold/extend, layout/PIP, captions, clip player controls, ST map, color grade, corrector, AI segment. Each action's `params` is a free-form `map[string]any`; the runner extracts fields by key with type assertions.

### Validation

`macro.ValidateSteps` walks the steps before save and returns errors (which block save) and warnings (informational). Errors include unknown actions, missing required params (source, layer ID, etc.), invalid enum values. Warnings include things like a reference to a source that doesn't currently exist (the source might come back).

### Runner

`macro.Run(ctx, macro, target, onProgress)` executes the steps sequentially. For each step:

1. Check context cancellation. If cancelled, mark current step `failed` with error `cancelled`, mark all subsequent steps `skipped`, return.
2. Mark step `running` and notify via `onProgress`.
3. For `wait` steps, populate `WaitMs` and `WaitStartMs` before notifying so the UI can render a countdown.
4. Call `executeStep` which dispatches on the action. For most actions, this calls into the `Target` interface (which wraps switcher, mixer, compositor, etc.).
5. On success, mark `done` and notify.
6. On failure, mark `failed`, mark subsequent steps `skipped`, notify, and return.

The runner never blocks on the HTTP response context — `handleRunMacro` explicitly creates a background `context.Background()`-derived context so the macro continues even after the response is sent.

### Execution progress broadcasting

The `onProgress` callback given to the runner updates `(*API).macroState` (under `macroMu`) and calls `broadcast()`. The state is a `MacroExecutionState` with the list of `MacroStepState` values. Clients see each step's status evolve in real time via the broadcast.

Concurrency: only one macro runs at a time (enforced by the `macroState != nil && Running` check in `handleRunMacro`). The `macroGen` counter is bumped on every start so that when the runner finishes, the cleanup code only clears state if the generation still matches — preventing a race where a second macro starts between an earlier macro's completion and its cleanup.

Dismiss (`DELETE /api/macros/execution`) explicitly cancels any running macro too. Otherwise a dismiss without cancel would leave the background goroutine silently continuing to execute steps after the operator thinks they've stopped it.

## Gotchas and invariants

- **Every mutating handler calls `setLastOperator(r)` and/or `broadcast()`.** Forgetting either is a silent bug that shows up as UI staleness or incorrect operator badges. The convention is: call `setLastOperator(r)` at the top of the handler, call `broadcast()` after the mutation lands.
- **`broadcast()` is safe to call when nothing is registered.** It checks the atomic pointer.
- **Subsystem adapters (`AudioMixerAPI`, etc.) are interfaces, not concrete types.** This is deliberate — tests wire up mocks. Never add a method on a concrete subsystem type and expect the API to pick it up; add it to the interface.
- **The operator middleware is only installed when both `operatorStore` and `sessionMgr` are configured.** Demo mode skips it entirely.
- **`operator.EndpointSubsystem` uses exact match before prefix match.** When adding a new endpoint, decide whether it needs an exact entry (specific verbs on a shared prefix route to different subsystems) or whether the prefix entry covers it.
- **Preset recall is not atomic.** A preset recall that partially fails leaves the switcher in the partially-applied state. The warnings array is how the operator discovers what didn't apply.
- **Macro run context is `context.Background()`, not `r.Context()`.** The HTTP response completes before the macro finishes; using the request context would cancel the macro as soon as the response was sent. This is documented inline in `handleRunMacro` but easy to miss in a refactor.
- **Per-operator tokens are 64-char hex, session API token is also 64-char hex.** Timing-safe compare handles both.
- **Force-leader does not go through `AuthMiddleware`** — it's in `authExemptPaths` because it uses the separate `X-Control-Plane-Secret` header. When no secret is configured, the handler returns 501 regardless of the header.
- **Rolling back a failed preset/operator/macro save leaves the in-memory slice consistent.** The stores all follow the same pattern: mutate memory, try to persist, roll back memory on persist failure, release the lock.

## Related docs

- **Concepts:** [pipeline.md](../pipeline.md) · [locking-and-concurrency.md](../locking-and-concurrency.md)
- **Reference:** [api.md](../reference/api.md) · [state-broadcast.md](../reference/state-broadcast.md) · [fast-control.md](../fast-control.md)
- **Subsystems:** [switcher.md](switcher.md) · [output.md](output.md) · [graphics-and-dve.md](graphics-and-dve.md) · [playout.md](playout.md)
- **Integration:** [ui-server-contract.md](../integration/ui-server-contract.md)
- **Operations:** [deployment.md](../deployment.md)
