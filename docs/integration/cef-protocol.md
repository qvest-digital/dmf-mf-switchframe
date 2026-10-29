# CEF sidecar protocol

> **Where you are:** [docs](../README.md) → integration → cef-protocol
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [subsystems/graphics-and-dve.md](../subsystems/graphics-and-dve.md) · [reference/api/graphics-html5.md](../reference/api/graphics-html5.md) · [reference/api/graphics.md](../reference/api/graphics.md)

**TL;DR** The CEF (Chromium Embedded Framework) sidecar is a separate C++ process the engine spawns to render HTML5 graphics into the DSK compositor. The engine talks to it over two Unix domain sockets — a newline-delimited JSON control socket for commands / responses / events, and a length-prefixed binary frame socket for the BGRA pixel data. A pool of N offscreen-rendered browser instances inside the sidecar takes engine-supplied URLs, injects a small [`adapter.js`](../../cef-sidecar/resources/adapter.js) shim that auto-detects the template format (OGraf, CasparCG, raw HTML), exposes a unified `play / stop / update / custom` surface, and paints into the engine's [`graphics.Compositor`](../../server/graphics/compositor.go) as a DSK overlay. If the sidecar crashes, the engine restarts it; if it can't be found, HTML5 graphics gracefully degrade off and the rest of the engine keeps running.

## The story

Broadcast graphics come in two flavors. The first is server-rendered: a PNG, an animated PNG sequence, a scrolling ticker, animated text with an embedded Inter font — all handled by the Go-side compositor in [`server/graphics/`](../../server/graphics/), documented in [subsystems/graphics-and-dve.md](../subsystems/graphics-and-dve.md). That path is lean, deterministic, and never touches a browser engine.

The second is browser-rendered: lower thirds with rich CSS animations, [OGraf](https://ograf.org) templates driven by a schema-based API, legacy CasparCG HTML templates that were written against `window.play()` / `window.stop()` globals, bespoke broadcast plant HTML. These need a full browser runtime, which means Chromium — which means CEF. Running CEF in-process with Go is a minefield: CEF multi-process crashes, GPU-process interactions, and Chromium's threading model all conflict with Go's goroutine-scheduled runtime. So the sidecar lives out-of-process, talks to the engine over Unix domain sockets, and handles every browser concern on its own terms.

The design target is: the engine should be able to treat "browser graphics" the same way it treats "static PNG graphics" — upload the rendered pixels to a DSK overlay slot, and that's it. The CEF sidecar is the machinery that turns an arbitrary URL into the same RGBA byte stream a PNG gives us. Everything the engine sees from it is BGRA pixels plus a few lifecycle events.

## How it works

```mermaid
flowchart LR
  subgraph engine ["Go engine (switchframe)"]
    API[REST /api/html-graphics/*]
    Mgr[htmlgfx.Manager]
    Comp[graphics.Compositor<br/>DSK overlay]
    Host[sidecar.Host<br/>spawn + restart]
  end

  subgraph ipc ["Unix domain sockets"]
    Ctl[control.sock<br/>JSON newlines]
    Frm[frames.sock<br/>length-prefixed BGRA]
  end

  subgraph cef ["CEF sidecar (C++)"]
    Main[main.cpp]
    CtlSock[ControlSocket]
    FrmSock[FrameSocket]
    Pool[BrowserPool<br/>N browsers]
    B1[Browser 0<br/>OffscreenRenderHandler]
    B2[Browser 1]
    Bn[Browser N-1]
    Adapter[adapter.js<br/>(injected)]
    Tpl[HTML template]
  end

  API --> Mgr
  Mgr --> Host
  Host --> Main
  Mgr --> Ctl
  Ctl --> CtlSock
  CtlSock --> Pool
  Pool --> B1
  Pool --> B2
  Pool --> Bn
  B1 --> Tpl
  B1 --> Adapter
  Adapter -.reportFormat.-> CtlSock
  CtlSock --> Ctl
  Ctl --> Mgr
  B1 --> FrmSock
  B2 --> FrmSock
  Bn --> FrmSock
  FrmSock --> Frm
  Frm --> Mgr
  Mgr --> Comp
```

### Process model

The sidecar binary ships as `switchframe-cef` (built from [`cef-sidecar/src/`](../../cef-sidecar/src/) via CMake — see [`cef-sidecar/CMakeLists.txt`](../../cef-sidecar/CMakeLists.txt)). The engine spawns it at startup through the shared [`sidecar.Host`](../../server/sidecar/) supervisor:

1. The engine creates a temporary socket directory (typically `/tmp/switchframe-cef-<random>`), starts listening on `control.sock` and `frames.sock` inside it, and forks the sidecar with `--control-socket <path> --frame-socket <path> --user-data-dir <path>`.
2. The sidecar process calls `CefExecuteProcess` first — CEF's multi-process architecture means the same binary runs as the main process *and* as renderer / GPU / utility subprocesses. Subprocess invocations return an exit code immediately and the main process continues past that check.
3. Main-process startup initializes CEF with windowless rendering enabled (`settings.windowless_rendering_enabled = true`, `no_sandbox = true`, transparent background), creates the `ControlSocket` and `FrameSocket` listeners, spawns the `BrowserPool`, and runs `CefRunMessageLoop()` on the main thread.
4. The engine's [`htmlgfx.Manager`](../../server/htmlgfx/manager.go) connects to both sockets, spawns its read loops, and is ready to issue commands.

On crash, the `sidecar.Host` observes the process exit, triggers a restart, and reconnects both sockets. The engine keeps running through the gap — browsers are transient, not durable state, and the operator sees a brief graphics blackout at worst.

If the sidecar binary isn't found at startup (`switchframe-cef` beside the `switchframe` binary, or the `--cef-binary` path), `Manager.Start` logs a warning and returns `nil`. The sidecar is spawned only with `--html5-graphics=true`; without it the spawn is skipped the same way. The rest of the engine works; every HTML5 graphics REST endpoint but the template list returns 503 until the sidecar is available.

### The browser pool

The sidecar keeps a fixed-size pool of browser instances of `N = 4`, a constant on both sides: `PoolSize` in the engine's [`buildHTMLGfxConfig`](../../server/cmd/switchframe/app.go) and the `BrowserPool` size in [`main.cpp`](../../cef-sidecar/src/main.cpp). Each browser is an offscreen-rendered Chromium tab with its own `OffscreenRenderHandler` that captures paint events. Browsers are addressed by their pool slot (`browser_id: 0 .. N-1`), not by URL; the engine recycles slots by first sending a `destroy` and then a fresh `create`.

A browser in the pool is in one of these logical states (tracked in [`server/htmlgfx/types.go`](../../server/htmlgfx/types.go)):

- **Empty** — no content loaded.
- **Created** — `create` succeeded; dimensions and FPS configured; no URL loaded yet.
- **Loading** — `load` sent; waiting for `loadEnd` event or timeout.
- **Loaded** — `loadEnd` received; `adapter.js` injected; format detection underway.
- **Playing** — `play` action dispatched through the adapter.
- **Error** — load failed, render error, etc.

State transitions happen on control-socket events from the sidecar (`loadEnd`, `loadError`, `format`, `title`) and on commands from the engine (`create`, `load`, `play`, `stop`, `update`, `custom`, `destroy`).

### Control socket: JSON commands and responses

The control socket is a single connected stream of newline-delimited JSON. Every line is either a **command** (engine → sidecar) or a **response / event** (sidecar → engine).

**Commands** are the message types implemented in [`server/htmlgfx/protocol.go`](../../server/htmlgfx/protocol.go) as `ControlCommand`:

| `cmd` | Fields | Purpose |
|---|---|---|
| `create` | `id`, `width`, `height`, `fps` | Allocate pool slot `id`, configure dimensions + target FPS |
| `load` | `id`, `url` | Navigate browser `id` to `url`; triggers `loadEnd` / `loadError` events |
| `play` | `id` | Call adapter's `play()` — dispatches to OGraf `playAction({})` or CasparCG `window.play()` |
| `stop` | `id` | Adapter's `stop()` — OGraf `stopAction({})` or CasparCG `window.stop()` |
| `update` | `id`, `data` | Adapter's `update(data)` — OGraf `updateAction({data})` or CasparCG `window.update(json)` |
| `custom` | `id`, `action`, `payload` | Adapter's `custom(action, payload)` — OGraf `customAction({id, payload})` only |
| `destroy` | `id` | Close the browser; slot returns to Empty |
| `shutdown` | (no id) | Shut down the entire sidecar |

Commands use `omitempty` on optional fields so a `create` message doesn't include unused `url` / `data` / `action` / `payload`. The `id` field is `*int` in Go specifically so `shutdown` (which has no id) marshals cleanly.

**Responses** are shaped as `ControlResponse`:

| Field | Meaning |
|---|---|
| `id` | Matches the command's `id` (omitted for async events) |
| `status` | `"ok"` on success, `"error"` on failure |
| `error` | Error message (when `status: "error"`) |
| `event` | Event name for async notifications (`loadEnd`, `loadError`, `format`, `title`) |
| `format` | Detected template format (`"ograf"`, `"casparcg"`, `"unknown"`) — emitted after `adapter.js` reportFormat fires |
| `data` | Payload for events that carry one (title text, load error text) |

Events don't have an `id`; they're spontaneous notifications. The engine's control-socket read loop routes them into browser state transitions.

The builder helpers live in [`cef-sidecar/src/protocol.h`](../../cef-sidecar/src/protocol.h) (`MakeOkResponse`, `MakeErrorResponse`, `MakeEventResponse`) and `MaxMessageSize` caps JSON at a sensible size to prevent runaway memory on malformed input.

### Frame socket: binary BGRA

The frame socket carries rendered pixels. Each frame is a length-prefixed binary message:

```
Byte   0 1 2 3    4     5 6      7 8      9 .. 8 + w*h*4
     ┌─────────┬──────┬────────┬────────┬───────────────┐
     │  len    │ id   │  W     │  H     │   BGRA rows   │
     │ u32 BE  │ u8   │ u16 BE │ u16 BE │  (top-down)   │
     └─────────┴──────┴────────┴────────┴───────────────┘
```

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | `total_length` (uint32 BE) — size of everything after these 4 bytes |
| 4 | 1 | `browser_id` (0 .. N-1) |
| 5 | 2 | `width` (uint16 BE, pixels) |
| 7 | 2 | `height` (uint16 BE, pixels) |
| 9 | W × H × 4 | BGRA rows, top-down, premultiplied alpha |

Header layout is documented in both [`server/htmlgfx/protocol.go`](../../server/htmlgfx/protocol.go) (`WriteFrame` / `ReadFrame`) and [`cef-sidecar/src/protocol.h`](../../cef-sidecar/src/protocol.h) (`SerializeFrame` / `ParseFrameHeader`) — the two definitions are kept in lockstep by convention.

`kMaxFramePixels = 7680 * 4320 * 4` (8K UHD) caps payload size on both sides; the Go reader rejects frames exceeding the cap without allocating a buffer for them (reads and discards the bytes from the socket to stay in sync). `FrameHeaderSize = 5` and `maxFramePixels` are the load-bearing constants; both sides assert matching values.

Pixel data is BGRA because that's what Chromium's offscreen render handler produces natively; the engine's [`graphics.Compositor`](../../server/graphics/compositor.go) converts to RGBA at the overlay boundary.

### adapter.js — the shim

Every browser in the pool has [`adapter.js`](../../cef-sidecar/resources/adapter.js) injected into every page just after `load`. The adapter is ~110 lines of vanilla JavaScript (no dependencies, IIFE-wrapped, strict mode) and it does two things:

1. **Format detection.** On document `load`, it scans the DOM for:
   - `<link rel="ograf-manifest">` with a custom element exposing `playAction`, `stopAction`, `updateAction`, `customAction` — format = `ograf`.
   - Otherwise `window.play` as a function — format = `casparcg`.
   - Otherwise `unknown`.
   Detection result is reported back to C++ via a `CefV8` binding (`window.__switchframe_native.reportFormat`), which the sidecar translates into a control-socket `format` event.

2. **Unified action surface.** It exposes `window.__switchframe.play()`, `stop()`, `next()`, `update(data)`, `custom(action, payload)`. Each method dispatches to the detected format — OGraf calls the custom element's methods with the OGraf schema; CasparCG calls `window.play()` / `window.stop()` / `window.update(jsonString)`. The C++ side calls these methods via CEF's `ExecuteJavaScript` when it handles a control-socket command.

This is how the engine speaks one unified protocol (`play`, `stop`, `update`, `custom`) to every template, regardless of which convention the HTML inside follows. Templates that use neither OGraf nor CasparCG conventions still load and render; they just don't respond to `play` / `stop` (which is fine for static HTML overlays).

The adapter also handles `</script>` injection defense when serializing strings for embedding in `<script>` tags, and JSON escape for data passed through CEF's V8 bridge.

### Graphics template conventions

The sidecar ships example templates in [`cef-sidecar/demo-templates/`](../../cef-sidecar/demo-templates/):

- `ograf-lower-third/` — the reference OGraf implementation with manifest, custom element, playAction / stopAction / updateAction schemas.
- `gsap-lower-third/` — a GreenSock-animated OGraf lower third.
- `news-ticker/`, `interview-nameplate/`, `sports-scorebug/`, `weather-bug/`, `chapter-marker/`, `chapter-title/`, `live-poll/`, `social-media-card/`, `podcast-lower-third/`, `donation-goal/`, `event-agenda/`, `election-results/`, `esports-player-card/`, `now-playing/`, `quotation-card/`, `countdown-timer/` — a catalog of OGraf templates spanning common broadcast graphics use cases.

Each template is a static directory with `index.html`, `manifest.json`, and any supporting JS/CSS/fonts/images. Operators host these on any HTTP server (or include them in the engine's embedded UI path) and reference them by URL when they `load` a browser slot.

[`cef-sidecar/test-templates/`](../../cef-sidecar/test-templates/) holds fixtures used by the integration tests in [`cef-sidecar/tests/`](../../cef-sidecar/tests/) — `solid-red.html` is the simplest possible template (a solid red page with no scripts), useful for exercising the full create → load → frame delivery path in isolation.

### REST surface

The engine exposes an HTTP API on top of the sidecar — see [reference/api/graphics-html5.md](../reference/api/graphics-html5.md) for the full list. The handlers live in [`server/control/api_graphics.go`](../../server/control/api_graphics.go). Endpoint highlights:

- `POST /api/html-graphics/create` — allocate a pool slot at given dimensions / fps
- `POST /api/html-graphics/{id}/load` — navigate to a URL
- `POST /api/html-graphics/{id}/play` / `stop`
- `PUT /api/html-graphics/{id}/update` — send a `data` payload to the adapter's `update()`
- `POST /api/html-graphics/{id}/custom` — fire an arbitrary named action with JSON payload
- `DELETE /api/html-graphics/{id}` — destroy the browser slot

The DSK overlay plumbing is shared with the rest of graphics — see [subsystems/graphics-and-dve.md](../subsystems/graphics-and-dve.md). When the sidecar delivers a frame on the frame socket, [`Manager`](../../server/htmlgfx/manager.go) calls `compositor.SetOverlay(id, rgba, width, height, template)` which updates the matching layer in the DSK compositor. The overlay is composited on top of program video the same way a PNG or animated PNG sequence would be — browser-origin is invisible at the YUV blend stage.

### Lifecycle events

```mermaid
sequenceDiagram
  participant Eng as Engine (Manager)
  participant Ctl as control.sock
  participant Cef as CEF Sidecar
  participant Frm as frames.sock
  participant Cmp as graphics.Compositor

  Eng->>Ctl: {"cmd":"create","id":0,"width":1920,"height":200,"fps":30}
  Ctl->>Cef: parse + create browser 0
  Cef->>Ctl: {"id":0,"status":"ok"}
  Ctl->>Eng: ok

  Eng->>Ctl: {"cmd":"load","id":0,"url":"https://gfx.example/lower-third/"}
  Cef->>Cef: navigate; inject adapter.js on load
  Cef->>Ctl: {"id":0,"event":"loadEnd"}
  Cef->>Ctl: {"id":0,"event":"format","format":"ograf"}
  Cef->>Frm: first BGRA frame
  Frm->>Eng: frame bytes
  Eng->>Cmp: SetOverlay(0, rgba, 1920, 200, "html5")
  Note over Cmp: layer composited onto program

  Eng->>Ctl: {"cmd":"play","id":0}
  Cef->>Cef: __switchframe.play() → adapter dispatch
  loop render ticks at target fps
    Cef->>Frm: BGRA frame
    Frm->>Eng: frame bytes
    Eng->>Cmp: SetOverlay(0, rgba, ...)
  end

  Eng->>Ctl: {"cmd":"destroy","id":0}
  Cef->>Cef: close browser 0
  Cef->>Ctl: {"id":0,"status":"ok"}
```

The frame cadence is driven by the target FPS the engine passed on `create`. CEF's offscreen renderer paints on every Chromium compositor frame; the sidecar throttles delivery to the target rate so an animated template doesn't produce more frames than the engine can composite.

## Key types and entry points

- **Engine side:**
  - [`server/htmlgfx/manager.go`](../../server/htmlgfx/manager.go) → `Manager`, `Start`, `Create`, `Load`, `Play`, `Stop`, `Update`, `Custom`, `Destroy`, `controlReadLoop`, `frameReadLoop`
  - [`server/htmlgfx/protocol.go`](../../server/htmlgfx/protocol.go) → `ControlCommand`, `ControlResponse`, `WriteControlCommand`, `readControlResponse`, `Frame`, `WriteFrame`, `ReadFrame`, `FrameHeaderSize`
  - [`server/htmlgfx/types.go`](../../server/htmlgfx/types.go) → `Config`, `BrowserState`
  - [`server/htmlgfx/convert.go`](../../server/htmlgfx/convert.go) → BGRA → RGBA conversion on the compositor boundary
  - [`server/htmlgfx/templates.go`](../../server/htmlgfx/templates.go) → template URL resolution helpers
  - [`server/sidecar/`](../../server/sidecar/) → `Host` — spawn + restart + socket lifecycle
- **Sidecar side:**
  - [`cef-sidecar/src/main.cpp`](../../cef-sidecar/src/main.cpp) → entry, arg parsing, CEF init, listener setup, message loop
  - [`cef-sidecar/src/app.cpp`](../../cef-sidecar/src/app.cpp) / `app.h` → `SwitchframeCefApp` (CefApp)
  - [`cef-sidecar/src/browser_pool.cpp`](../../cef-sidecar/src/browser_pool.cpp) / `browser_pool.h` → `BrowserPool`, `BrowserClient`, `OnAfterCreated`, `OnLoadEnd`, `OnLoadError`
  - [`cef-sidecar/src/render_handler.cpp`](../../cef-sidecar/src/render_handler.cpp) / `render_handler.h` → `OffscreenRenderHandler`, `OnPaint` → frame socket
  - [`cef-sidecar/src/control_socket.cpp`](../../cef-sidecar/src/control_socket.cpp) / `control_socket.h` → `ControlSocket` — accept / read / dispatch / `SendResponse`
  - [`cef-sidecar/src/frame_socket.cpp`](../../cef-sidecar/src/frame_socket.cpp) / `frame_socket.h` → `FrameSocket` — accept / write-frame
  - [`cef-sidecar/src/adapter.cpp`](../../cef-sidecar/src/adapter.cpp) / `adapter.h` → JavaScript adapter injection + CefV8 binding for `reportFormat`
  - [`cef-sidecar/src/json_parse.cpp`](../../cef-sidecar/src/json_parse.cpp) / `json_parse.h` → minimal JSON parser for control commands (no external deps)
  - [`cef-sidecar/src/protocol.h`](../../cef-sidecar/src/protocol.h) → wire format constants and serializers (mirrors `htmlgfx/protocol.go`)
- **Resources and templates:**
  - [`cef-sidecar/resources/adapter.js`](../../cef-sidecar/resources/adapter.js) → the injected adapter script
  - [`cef-sidecar/demo-templates/`](../../cef-sidecar/demo-templates/) → OGraf and legacy example templates
  - [`cef-sidecar/test-templates/`](../../cef-sidecar/test-templates/) → integration-test fixtures
- **Build + setup:**
  - [`cef-sidecar/CMakeLists.txt`](../../cef-sidecar/CMakeLists.txt), [`cef-sidecar/Makefile`](../../cef-sidecar/Makefile)
  - [`cef-sidecar/scripts/`](../../cef-sidecar/scripts/) -> bare-metal Linux setup; the sidecar selects Chromium's headless Ozone platform itself, so it needs no X server
  - [`Dockerfile.mxl`](../../Dockerfile.mxl) -> the `cef` stage: pinned CEF minimal distribution, stripped, installed under `/opt/switchframe-cef`
- **REST:** [`server/control/api_graphics.go`](../../server/control/api_graphics.go) (HTML5-specific handlers)

## Gotchas and invariants

- **CEF subprocesses share the binary.** `CefExecuteProcess` returns early in renderer / GPU / utility subprocess invocations. Don't put initialization code before `CefExecuteProcess` that assumes it's the main process — it'll run in every subprocess and corrupt them.
- **Windowless rendering mandatory.** The sidecar configures `settings.windowless_rendering_enabled = true` and `settings.no_sandbox = true`. Sandboxing is incompatible with windowless; disabling the sandbox means the sidecar binary must be run in a trusted environment (not as the web entry point for untrusted HTML). Operators who load arbitrary third-party URLs into the pool accept the security trade-off.
- **Transparent background is load-bearing.** `settings.background_color = CefColorSetARGB(0, 0, 0, 0)` makes the browser render with alpha channel. Without it, DSK overlays get a solid black background where they should be transparent.
- **Frame socket byte alignment.** The 4-byte length prefix is the outer framing; everything after it is the frame header + pixel data. `ReadFrame` uses `io.ReadFull` to avoid short reads. If the socket delivers partial bytes, the reader blocks until the full frame arrives or the connection closes.
- **Header format must match across Go and C++.** `FrameHeaderSize = 5` in [`protocol.go`](../../server/htmlgfx/protocol.go) and `kFrameHeaderSize = 5` in [`protocol.h`](../../cef-sidecar/src/protocol.h). Both sides cap `maxFramePixels` at 8K UHD. Changing either side requires changing both.
- **Control socket is single-connection.** The sidecar listens, the engine dials, and that's it. There's no multi-client multiplexing. The Go-side `sidecar.Host` owns the `net.Conn` and the manager's read loop owns scanning JSON lines.
- **Newline-delimited JSON has a size cap.** The control-socket reader uses a `bufio.Scanner` with a default buffer size. Commands with huge `data` or `payload` fields may exceed it. The scanner's buffer is raised in the manager; templates that need to ship megabytes of data per `update` should either chunk or fetch the payload themselves via HTTP rather than passing it through the control socket.
- **Graceful degradation when the binary is missing.** `Manager.Start` returns `nil` with a warning log if the sidecar binary doesn't exist. This is how a CPU-only Linux CI box can build and run Switchframe without CEF. REST endpoints that need the sidecar then return descriptive errors.
- **Restart resets the pool.** When the sidecar crashes, every browser in the pool is lost. `sidecar.Host.OnRestart` calls `Manager.resetBrowserPool` which marks every slot `Empty` and invalidates overlay data. The engine's HTML5 state broadcast then reflects the reset; operators see graphics go black and need to re-create / re-load each slot.
- **Browser IDs are pool slots, not globally unique.** After a `destroy`, the same ID is reused on the next `create`. Don't cache browser IDs across operator sessions as if they were stable identifiers for content.
- **BGRA top-down, pre-multiplied alpha.** Chromium's offscreen paint buffer is BGRA with pre-multiplied alpha, and row 0 is the top. The compositor's overlay path handles the conversion; direct consumers must account for this.
- **OGraf detection is heuristic.** The adapter looks for `<link rel="ograf-manifest">` + a custom element with `playAction`. Templates that declare a manifest but don't wire a playAction fall back to CasparCG detection (via `window.play`). The adapter's `format` event reports the final decision, so the engine knows whether `play` / `stop` / `update` / `custom` will reach a real handler.
- **Adapter load timing varies.** On templates with heavy initial JavaScript, adapter injection may race with the template's own load logic. The adapter hooks `document.readyState === 'complete'` with a 100 ms safety delay on the first call to give the page time to define its custom elements; slower templates may need a first `play` to arrive slightly later than load.
- **No CEF message-loop thread sharing.** Chromium's main thread owns the CEF message loop; the `ControlSocket` and `FrameSocket` own their own threads. Commands that change browser state (create, load, play) post tasks to the CEF UI thread via `CefPostTask(TID_UI, ...)` — don't call CEF API methods directly from the socket read threads or you'll crash the message loop.
- **`shutdown` over the control socket is the clean exit path.** The engine sends `{"cmd":"shutdown"}` on graceful stop; the sidecar quits the message loop and exits. The Host supervisor's disabled shutdown message flag prevents conflicting shutdown signals from two sources.
- **HEVC not yet wired.** The sidecar is BGRA-only and doesn't care about video codecs; the DSK overlay path handles either 8-bit YUV420 or 10-bit YUV422P10LE program frames identically because the overlay is always 8-bit RGBA at composition time.

## Related docs

- **Concepts:** [media-path](../concepts/media-path.md) · [pipeline](../concepts/pipeline.md) · [locking-and-concurrency](../concepts/locking-and-concurrency.md)
- **Reference:** [api](../reference/api.md) · [api/graphics-html5](../reference/api/graphics-html5.md) · [api/graphics](../reference/api/graphics.md) · [config-and-flags](../reference/config-and-flags.md) · [state-broadcast](../reference/state-broadcast.md)
- **Subsystems:** [graphics-and-dve](../subsystems/graphics-and-dve.md) · [switcher](../subsystems/switcher.md) · [playout](../subsystems/playout.md)
- **Integration:** [ui-server-contract](ui-server-contract.md) · [asr-sidecar](asr-sidecar.md)
- **Operations:** [deployment](../operations/deployment.md)
- **Root:** [architecture](../architecture.md) · [README](../README.md)
