# File Formats

> **Where you are:** [docs](../README.md) → reference → file-formats
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [config-and-flags.md](./config-and-flags.md) · [metrics.md](./metrics.md) · [../operations/deployment.md](../operations/deployment.md)

**TL;DR** Switchframe persists operator-created state — presets, macros, SRT sources, operators, clips, stingers, playout schedules, SCTE-35 rules — under `~/.switchframe/` (override with `SWITCHFRAME_STATE_DIR`). Most stores use JSON files with atomic writes (temp file + fsync + rename), a LIFO rollback on disk-write failure, and an RWMutex guard. ST maps and stinger cache use custom binary formats for size and speed. Playout adds an append-only JSONL log for as-run events and periodic JSON snapshots for crash recovery. Every file schema corresponds to a Go struct in the owning subsystem's `store.go`.

## The story

The filesystem is a configuration surface. Operators do not boot with a blank slate — they expect the PIP layout they saved, the macro they recorded, the camera they configured, and the lower-third PNG sequence they uploaded to still be there on restart. Switchframe's on-disk layout is the durable form of the runtime state the UI shows.

Three patterns recur across stores.

**Atomic-write JSON.** The canonical pattern is `os.CreateTemp` → `Write` → `Sync` (fsync) → `Close` → `Rename`. It guarantees readers never see a partial file — either the old complete file or the new complete file. Every mutating operation also keeps the pre-write in-memory state as a backup and restores it if any step fails. See [`preset.Store.save`](../../server/preset/store.go) for the reference implementation; [`operator.Store.writeFile`](../../server/operator/store.go), [`macro.Store.writeFile`](../../server/macro/store.go), [`srt.Store.flush`](../../server/srt/store.go), [`clip.Store.save`](../../server/clip/store.go), [`playout.Cache.persist`](../../server/playout/cache.go), and [`playout.atomicWriteJSON`](../../server/playout/persist.go) follow the same pattern.

**Lock ordering around disk I/O.** All these stores hold `mu` during `marshal + writeFile` so readers never see a transient state where the in-memory map and the on-disk file disagree. File I/O latency gets absorbed into the lock hold time; that's acceptable because file-backed stores are mutated by operator actions, not by the frame path.

**Binary for bulk, JSON for control.** Anything operators read or edit directly — presets, macros, rules — is JSON. Anything large or performance-critical — ST map LUTs (megabytes of float32 per frame), stinger mmap caches (hundreds of megabytes of decoded frames), cached media assets — uses a custom binary layout. ST maps use a 4-byte big-endian header and little-endian float32 planes; stinger caches use mmap-friendly fixed-layout records.

The rest of this doc walks each store, its file layout, its schema, and its invariants. Every schema field corresponds one-to-one to a Go struct field in the owning package.

## Directory layout

```
$SWITCHFRAME_STATE_DIR (default ~/.switchframe/)
├── presets.json              — preset.Store
├── macros.json               — macro.Store
├── operators.json            — operator.Store
├── srt_sources.json          — srt.Store
├── scte35-rules.json         — scte35.RulesStore
├── clips/
│   ├── clips.json            — clip.Store metadata
│   └── <random-hex>.ts       — clip media files
├── stingers/
│   └── <name>/
│       ├── frame-NNNN.png    — stinger PNG sequence
│       └── audio.wav         — optional stinger audio
├── wipe_patterns/
│   └── <name>                — wipemap.WipeMapStore
├── dve_presets/
│   └── <id>.json             — dve.PresetStore
├── stmaps/
│   ├── <name>.stmap          — static ST map (binary)
│   └── <name>.stmap-anim.json — animated ST map metadata
├── luts/                     — colorgrade.Store (.cube / .json)
└── playout/
    ├── cache_meta.json        — playout.Cache LRU index
    ├── playout-state.json     — playout.StatePersister crash snapshot
    ├── assets/                — SHA-256-named cached media
    └── asrun/
        └── asrun-YYYY-MM-DD.jsonl — daily as-run event log
```

Persistent cache (`--cache-dir`, defaults `/data/switchframe/cache` on Linux, `~/.switchframe/cache` on macOS):

```
$CACHE_DIR/
├── stingers/<name>.bin        — stinger.Store mmap cache
├── stmap/<name>_<hash>/       — stmap mmap cache
└── models/                    — AI model working files
```

## Presets

**File:** `~/.switchframe/presets.json`
**Owner:** [`preset.Store`](../../server/preset/store.go)
**Format:** JSON array of `Preset` objects, sorted by `CreatedAt` ascending.

```json
[
  {
    "id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
    "name": "Two Camera Mix",
    "programSource": "cam1",
    "previewSource": "cam2",
    "transitionType": "mix",
    "transitionDurMs": 1000,
    "audioChannels": {
      "cam1": { "level": 0.0, "muted": false, "afv": true },
      "cam2": { "level": -6.0, "muted": false, "afv": false }
    },
    "masterLevel": -3.0,
    "createdAt": "2026-04-15T14:22:08.123456Z"
  }
]
```

| Field | Go type | Description |
|---|---|---|
| `id` | `string` | UUIDv4 |
| `name` | `string` | Display name (must be non-empty) |
| `programSource` | `string` | Source key for program bus |
| `previewSource` | `string` | Source key for preview bus |
| `transitionType` | `string` | Last transition type (e.g., `mix`, `dip`, `ftb`) |
| `transitionDurMs` | `int` | Transition duration in milliseconds |
| `audioChannels` | `map[string]AudioChannelPreset` | Per-source audio snapshot |
| `audioChannels[*].level` | `float64` | Fader in dB |
| `audioChannels[*].muted` | `bool` | Mute state |
| `audioChannels[*].afv` | `bool` | Audio-follows-video state |
| `masterLevel` | `float64` | Master fader in dB |
| `createdAt` | `time.Time` | RFC3339 with nanosecond precision |

Recall via `POST /api/presets/{id}/recall` writes back all fields to the runtime state.

## Macros

**File:** `~/.switchframe/macros.json`
**Owner:** [`macro.Store`](../../server/macro/store.go)
**Format:** JSON array of `Macro` objects.

```json
[
  {
    "name": "opening-sequence",
    "steps": [
      { "action": "cut", "params": { "source": "cam1" } },
      { "action": "wait", "params": { "ms": 2000 } },
      { "action": "graphics_fly_in", "params": { "layerId": 0, "direction": "left" } },
      { "action": "transition", "params": { "type": "mix", "durationMs": 1500, "source": "cam2" } }
    ]
  }
]
```

| Field | Go type | Description |
|---|---|---|
| `name` | `string` | Unique macro name (primary key) |
| `steps` | `[]Step` | Ordered sequence of actions |
| `steps[*].action` | `Action` (string) | Must match one of ~80 declared actions in [`macro/types.go`](../../server/macro/types.go) |
| `steps[*].params` | `map[string]any` | Action-specific parameters (JSON values) |

Validation: `Save` rejects empty names and empty step lists, runs [`ValidateSteps`](../../server/macro/validate.go) on the steps, and returns the first validation error if any.

## Operators

**File:** `~/.switchframe/operators.json`
**Owner:** [`operator.Store`](../../server/operator/store.go)
**Format:** JSON array of `Operator` objects.

```json
[
  {
    "id": "a1b2c3d4e5f6a7b8",
    "name": "Alice",
    "role": "director",
    "token": "2f3c1a...64chars"
  }
]
```

| Field | Go type | Description |
|---|---|---|
| `id` | `string` | 16-byte hex (32 chars) or caller-provided 8–64 char alphanumeric |
| `name` | `string` | Display name (must be unique per store) |
| `role` | `operator.Role` | One of `director`, `audio`, `graphics`, `captioner`, `viewer` |
| `token` | `string` | 32-byte hex (64 chars) bearer token |

On load, the store rebuilds a `token → index` map and an `id → index` map for O(1) lookup. Roles and subsystem locks are defined in [`operator/types.go`](../../server/operator/types.go) with an explicit permission matrix (`rolePermissions`, `roleLockable`).

## SRT sources

**File:** `~/.switchframe/srt_sources.json`
**Owner:** [`srt.Store`](../../server/srt/store.go)
**Format:** JSON object wrapping a map.

```json
{
  "sources": {
    "srt:camera1": {
      "key": "srt:camera1",
      "mode": "caller",
      "address": "198.51.100.10:7001",
      "streamID": "live/camera1",
      "label": "Camera 1",
      "position": 0,
      "latencyMs": 120,
      "delayMs": 0,
      "inputBW": 0,
      "overheadBW": 25
    },
    "srt:camera2": {
      "key": "srt:camera2",
      "mode": "listener",
      "streamID": "live/camera2"
    }
  }
}
```

| Field | Go type | Description |
|---|---|---|
| `key` | `string` | Must start with `srt:`. Primary key |
| `mode` | `string` | `caller` (outbound pull) or `listener` (accept push) |
| `address` | `string` | Remote `host:port` (required for caller mode) |
| `streamID` | `string` | SRT stream ID / key (required) |
| `label` | `string` | Display label (optional) |
| `position` | `int` | UI ordering (optional) |
| `latencyMs` | `int` | SRT latency 0–10000 ms |
| `delayMs` | `int` | Per-source lip-sync delay 0–500 ms |
| `inputBW` | `int64` | Estimated input bandwidth in bytes/sec (0 = auto) |
| `overheadBW` | `int` | Overhead bandwidth percent 0–100 (0 = srtgo default 25) |

Validation ([`SourceConfig.Validate`](../../server/srt/types.go)) enforces all ranges; errors wrap `srt.ErrInvalidConfig` so the API layer can return 400 Bad Request.

## Clips

**Directory:** `~/.switchframe/clips/`
**Metadata file:** `clips.json` (JSON array of `Clip` objects)
**Media files:** `<random-hex>.ts` (or `.mp4`, `.mov`) alongside the metadata
**Owner:** [`clip.Store`](../../server/clip/store.go), [`clip.Types`](../../server/clip/types.go)

```json
[
  {
    "id": "9f3a...32chars",
    "name": "Opening Montage",
    "filename": "9f3a...32chars.ts",
    "source": "upload",
    "codec": "h264",
    "audioCodec": "aac",
    "width": 1920,
    "height": 1080,
    "fpsNum": 30000,
    "fpsDen": 1001,
    "durationMs": 15400,
    "sampleRate": 48000,
    "channels": 2,
    "byteSize": 24576000,
    "loop": false,
    "createdAt": "2026-04-12T09:11:44.512Z",
    "ephemeral": false
  }
]
```

| Field | Go type | Description |
|---|---|---|
| `id` | `string` | 16-byte hex (32 chars), generated at Add |
| `name` | `string` | Display name |
| `filename` | `string` | File name relative to clips dir |
| `source` | `ClipSource` | One of `upload`, `replay`, `recording` |
| `codec` | `string` | `h264` or `h265` |
| `audioCodec` | `string` | `aac` (optional) |
| `width`, `height` | `int` | Dimensions (must be even) |
| `fpsNum`, `fpsDen` | `int` | Rational frame rate |
| `durationMs` | `int64` | Length in milliseconds |
| `sampleRate` | `int` | Audio sample rate (optional) |
| `channels` | `int` | Audio channel count (optional) |
| `byteSize` | `int64` | File size; counted against `--clip-storage-max` for non-ephemeral clips |
| `loop` | `bool` | Whether loop-at-end is armed |
| `createdAt` | `time.Time` | RFC3339 |
| `ephemeral` | `bool` | If true, subject to TTL cleanup (`--clip-ephemeral-ttl`) |
| `libraryPath` | `string` | Media path relative to `--clip-library-dir` for a library clip, which then has no `filename` and is never TTL-cleaned (optional) |

A library clip's ID is its file's stem when that is a clip ID, otherwise derived from its path in the library, so every engine scanning one library assigns the same IDs. Next to each library media file `<file>.json` holds the same fields without `id`, `filename` and `libraryPath`, so a scan does not probe the media again; it is ignored when its `byteSize` no longer matches the file. While a clip is copied in, `<file>.pending` sits beside it: a scan skips media with a marker, since a copy cut short by a crash still leaves a shorter file, and the next copy of that clip, retried at start for clips still stored locally, replaces it.

Ephemeral clips bypass the storage quota check on Add and are deleted by `CleanEphemeral` if older than `maxAge` and not currently loaded in any player slot.

## Stingers

**Directory:** `~/.switchframe/stingers/<name>/`
**Media:** PNG sequence (`frame-NNNN.png`, sorted lexically) + optional single WAV audio file
**Cache:** `<cache-dir>/stingers/<name>.bin` (mmap-backed decoded frame cache)
**Owner:** [`stinger.Store`](../../server/stinger/store.go)

Stingers are uploaded as ZIP archives containing PNG frames (1–300 per clip, with anti-zip-bomb limits) and an optional WAV file. The store extracts the ZIP into `<name>/`, decodes each PNG to YUV420 + alpha ([`RGBAToFrame`](../../server/stinger/store.go)), loads the WAV ([`ParseWAV`](../../server/stinger/wav.go), supports PCM int16 and IEEE float32), and optionally persists an mmap-friendly binary cache.

| Aspect | Value |
|---|---|
| PNG count limit | 300 frames per clip (`maxFramesPerClip`) |
| Max PNG size | 50 MB per file (`maxZipEntrySize`) |
| Max total extracted | 500 MB per upload (`maxTotalExtractedBytes`) |
| Max clips in store | 16 by default (`maxClips`) |
| Supported WAV formats | PCM int16, IEEE float32 |
| Default cut point | 0.5 (halfway through the sequence) |

Clip names must not contain path separators and must not be `.` or `..` (enforced by [`validateName`](../../server/stinger/store.go)).

## DVE presets

**Directory:** `~/.switchframe/dve_presets/`
**Files:** `<id>.json`, one per preset
**Owner:** [`dve.PresetStore`](../../server/dve/preset.go)

```json
{
  "id": "pip-top-right",
  "name": "PIP Top Right",
  "slots": [
    {
      "transform": { /* dve.Transform */ },
      "zOrder": 1,
      "enabled": true
    }
  ],
  "transitionMs": 300,
  "easing": "ease-in-out"
}
```

| Field | Go type | Description |
|---|---|---|
| `id` | `string` | Must not contain path separators; used as filename |
| `name` | `string` | Display name |
| `slots` | `[]Slot` | DVE slot list (per-slot transform, z-order, enabled flag) |
| `transitionMs` | `int` | Default recall transition duration |
| `easing` | `string` | Default easing name (e.g., `ease-in-out`) |

Built-in presets (not persisted — resolved at runtime): `pip-top-right`, `pip-top-left`, `pip-bottom-right`, `pip-bottom-left`, `side-by-side`, `quad`, `three-box`, `over-shoulder`, all in [`dve/preset.go`](../../server/dve/preset.go).

## ST maps

**Directory:** `~/.switchframe/stmaps/`
**Owner:** [`stmap.Store`](../../server/stmap/store.go)

Two file types:

### Static maps (`<name>.stmap`)

Custom binary format:

| Offset | Bytes | Value |
|---|---|---|
| 0 | 4 | `width` (uint32 big-endian) |
| 4 | 4 | `height` (uint32 big-endian) |
| 8 | `width*height*4` | S plane as little-endian float32 |
| `8 + w*h*4` | `width*height*4` | T plane as little-endian float32 |

Total file size: `8 + width*height*8` bytes.

### Animated maps (`<name>.stmap-anim.json`)

Metadata only; frames are regenerated from the generator at load time.

```json
{
  "generator": "vortex",
  "params": { "radius": 0.5, "strength": 1.2 },
  "width": 1920,
  "height": 1080,
  "frameCount": 120,
  "fps": 30
}
```

| Field | Go type | Description |
|---|---|---|
| `generator` | `string` | One of the registered generator names in [`stmap/generator.go`](../../server/stmap/generator.go) |
| `params` | `map[string]float64` | Generator-specific parameters |
| `width`, `height` | `int` | Map dimensions (usually pipeline resolution) |
| `frameCount` | `int` | Number of frames in the animation |
| `fps` | `int` | Playback rate |

Map names are validated by [`ValidateName`](../../server/stmap/types.go) — no path separators, no `.` or `..`.

## SCTE-35 rules

**File:** `~/.switchframe/scte35-rules.json`
**Owner:** [`scte35.RulesStore`](../../server/scte35/rules_store.go)

```json
{
  "rules": [
    {
      "id": "abc123...",
      "name": "Block local ads",
      "enabled": true,
      "priority": 10,
      "conditions": [
        { "field": "segmentation_upid", "operator": "starts_with", "value": "local:" }
      ],
      "logic": "and",
      "action": "delete"
    },
    {
      "id": "def456...",
      "name": "Rewrite duration",
      "enabled": true,
      "conditions": [
        { "field": "duration_ms", "operator": "lt", "value": "15000" }
      ],
      "action": "replace",
      "replaceWith": {
        "duration": 15000000000,
        "eventID": 42
      },
      "destinations": ["srt-primary", "srt-backup"]
    }
  ],
  "defaultAction": "pass"
}
```

| Field | Go type | Description |
|---|---|---|
| `rules[*].id` | `string` | Random identifier (generated on insert) |
| `rules[*].name` | `string` | Display name |
| `rules[*].enabled` | `bool` | Master toggle |
| `rules[*].priority` | `int` | Higher priority evaluates first (ties by order) |
| `rules[*].conditions` | `[]RuleCondition` | One or more match conditions |
| `rules[*].conditions[*].field` | `string` | SCTE-35 field to inspect |
| `rules[*].conditions[*].operator` | `string` | `eq`, `neq`, `starts_with`, `contains`, `lt`, `gt`, regex |
| `rules[*].conditions[*].value` | `string` | Comparison value (string form; parsed by operator) |
| `rules[*].logic` | `string` | `and` (default) or `or` combining multiple conditions |
| `rules[*].action` | `string` | `pass`, `delete`, or `replace` |
| `rules[*].replaceWith.duration` | `time.Duration` | Override cue duration (for `replace`) |
| `rules[*].replaceWith.eventID` | `uint32` | Override event ID (for `replace`) |
| `rules[*].destinations` | `[]string` | Optional destination restrictions |
| `defaultAction` | `string` | Fallback when no rule matches (`pass` default) |

Backward compat: singular `condition` object is still accepted via a custom `UnmarshalJSON` and promoted to the `conditions` slice.

## Playout asset cache

**File:** `<playout-dir>/cache_meta.json`
**Owner:** [`playout.Cache`](../../server/playout/cache.go)

```json
[
  {
    "asset_uri": "s3://switchframe-assets/promos/promo-01.mp4",
    "local_path": "/data/switchframe/playout/cache/assets/9a8b...ext",
    "state": 3,
    "byte_size": 58720256,
    "checksum": "sha256:...",
    "transcoded": true,
    "last_accessed": "2026-04-10T18:01:22Z",
    "fetched_at": "2026-04-10T12:00:03Z"
  }
]
```

| Field | Go type | Description |
|---|---|---|
| `asset_uri` | `string` | Primary key; source URI |
| `local_path` | `string` | Absolute path (must be under cache dir) |
| `state` | `AssetState` | `0` pending, `1` fetching, `2` transcoding, `3` cached, `4` error |
| `byte_size` | `int64` | File size on disk |
| `checksum` | `string` | Optional integrity checksum |
| `transcoded` | `bool` | True if the local copy has been transcoded to pipeline format |
| `last_accessed` | `time.Time` | LRU timestamp (touched on Get, not on Peek) |
| `fetched_at` | `time.Time` | When fetch completed |
| `error_msg` | `string` | Error text for `AssetError` state |

The cache file stores entries sorted by `last_accessed` for determinism. LRU eviction runs on Put when `byte_size` sum exceeds `MaxBytes`; entries in `AssetFetching` or `AssetTranscoding` are skipped during eviction to avoid removing a file that another goroutine is still writing.

## Playout as-run log

**Directory:** `<playout-dir>/asrun/`
**File pattern:** `asrun-YYYY-MM-DD.jsonl` (one file per UTC day)
**Format:** JSON Lines (one `AsRunEvent` per line), append-only
**Owner:** [`playout.AsRunLogger`](../../server/playout/asrun.go)

```json
{"ts":"2026-04-15T22:00:00Z","type":"item_start","channel_id":0,"item_id":"item-042","title":"Episode 101","scheduled_ts":"2026-04-15T22:00:00Z","delta_ms":12.3,"source":"cache://local/ep-101.ts"}
{"ts":"2026-04-15T22:29:58Z","type":"item_end","channel_id":0,"item_id":"item-042","actual_duration_ms":1798000}
{"ts":"2026-04-15T22:30:00Z","type":"pod_operation","channel_id":0,"pod_id":"pod-01","operation":"fire","operator":"director:alice"}
{"ts":"2026-04-15T22:30:04Z","type":"splice_active","channel_id":0,"target_pts":2700000,"actual_pts":2700360,"black_detect":{"found":true,"luma_mean":3.2,"offset_ms":4.0}}
```

Event types: `item_start`, `item_end`, `secondary_event_fire`, `splice_active`, `pod_operation`, `manual_override`, `cache_event`, `error`. All events share a common `timestamp`, `type`, `channel_id`, and optional fields that depend on the event type. See [`AsRunEvent`](../../server/playout/types.go) for the full union.

The logger keeps an in-memory ring buffer (default 10,000 events) for fast queries and a JSONL append file on disk. `CleanupOldFiles` deletes files older than `RetentionDays` on a daily cadence; set the retention via `--playout-asrun-retention`.

## Playout crash-recovery state

**File:** `<playout-dir>/playout-state.json`
**Owner:** [`playout.StatePersister`](../../server/playout/persist.go)
**Cadence:** Every `persist interval` (default 5 s) plus a final save on graceful shutdown.

```json
{
  "version": 1,
  "timestamp": "2026-04-15T22:14:03Z",
  "channels": [
    {
      "id": 0,
      "state": 2,
      "mode": 0,
      "cursor": 7,
      "item_start_time": "2026-04-15T22:12:00Z",
      "playlist": { /* full playout.Playlist */ },
      "pods": [
        {
          "id": "pod-01",
          "config": { /* full playout.PodConfig */ },
          "state": 3,
          "armed": true,
          "elapsed_ms": 120500,
          "fired_at": 1744754022000,
          "scte35_event_id": 42
        }
      ]
    }
  ],
  "asrun_file": "/data/switchframe/playout/asrun/asrun-2026-04-15.jsonl"
}
```

| Field | Go type | Description |
|---|---|---|
| `version` | `int` | Schema version; current is `1` — loader rejects anything else |
| `timestamp` | `time.Time` | When the snapshot was written |
| `channels[*].id` | `int` | Channel index (0 or 1) |
| `channels[*].state` | `ChannelState` | Runtime state enum |
| `channels[*].mode` | `OnAirMode` | Live vs cued mode |
| `channels[*].cursor` | `int` | Current item index |
| `channels[*].item_start_time` | `time.Time` | Wall-clock item start (used to adjust elapsed on resume) |
| `channels[*].playlist` | `*Playlist` | Full playlist (standby may not share storage) |
| `channels[*].pods` | `[]PersistedPod` | Per-pod state |
| `asrun_file` | `string` | Path to the active as-run JSONL file |

## Color grading looks

**Directory:** `~/.switchframe/luts/`
**Owner:** [`colorgrade.Store`](../../server/colorgrade/store.go)
**Format:** Mix of `.cube` (Adobe LUT format) and `.json` (Switchframe look metadata referencing a `.cube` file).

Each look defines a 3D LUT (typically 33×33×33 float entries) and metadata like default intensity. See the colorgrade package for details.

## Wipe patterns

**Directory:** `~/.switchframe/wipe_patterns/`
**Owner:** [`wipemap.WipeMapStore`](../../server/transition/wipemap/store.go)

Stores gradient maps used as shape/timing masks for wipe transitions. The store registers built-in patterns (linear, radial, clock, iris) at startup, then merges any user-uploaded maps from the directory. Format is image data (typically grayscale PNG) interpreted as a gradient: each pixel's luma value determines when that location transitions.

## Prism distribution state

When the server starts, [`certs.Generate`](../../server/cmd/switchframe/app.go) creates a self-signed TLS certificate valid for 14 days — used by Prism's WebTransport/MoQ server. External certificates (via `--tls-cert`/`--tls-key`) are loaded instead; no caching on disk.

## Gotchas and invariants

- **Atomic writes are not transactional across stores.** If you are mutating presets and macros together (e.g., from a macro step), a crash between the two writes leaves partial state. The UI reconciles on next read — there is no cross-store WAL.
- **Legacy DVE preset IDs.** Files written before the `id` field was added use the filename (minus `.json`) as the ID. [`PresetStore.List`](../../server/dve/preset.go) applies this fallback.
- **SRT store includes a non-persisted field.** `HWDeviceCtx` on `SourceConfig` is an `unsafe.Pointer` for NVDEC hardware device context. It is marked `json:"-"` so it never round-trips through disk — restart drops hardware acceleration until the runtime re-wires it.
- **Clip ByteSize is not re-verified at load.** On startup the store loads metadata and trusts the recorded size; the file itself is checked lazily when the clip is loaded into a player slot. A missing media file surfaces as a decode error at play time, not at store load.
- **Stinger name validation rejects separators and dots.** `validateName` rejects anything containing `/` or `\`, or names that are exactly `.` or `..`, and also rejects names where `filepath.Base(name) != name`. This prevents ZIP path traversal during upload.
- **Animated ST map regeneration happens at startup.** The metadata JSON carries generator name + params; the actual S/T planes are regenerated by calling the registered generator with the same params. If the generator changes semantics, existing animated maps change visually.
- **Playout state version is strict.** `loadPersistedState` rejects any `version != 1`. Forward-compatible changes must bump the version and add a migration path.
- **As-run JSONL files are append-only.** Do not edit them. The retention sweeper deletes files; it never truncates or rewrites.
- **LRU cache file path must be under cache dir.** `Cache.validatePath` rejects paths that `filepath.Rel` cannot express under the cache dir or whose relative form starts with `..`.
- **Operator deletion rebuilds both index maps.** On `Delete`, the store calls `rebuildIndices` after removing — writes proceed through atomic temp-file + rename. A crash mid-delete leaves the pre-delete state intact.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md), [locking-and-concurrency.md](../concepts/locking-and-concurrency.md), [gpu.md](../concepts/gpu.md), [media-path.md](../concepts/media-path.md)
- **Reference:** [config-and-flags.md](./config-and-flags.md), [metrics.md](./metrics.md), [api.md](./api.md), [state-broadcast.md](./state-broadcast.md)
- **Subsystems:** [clips.md](../subsystems/clips.md), [playout.md](../subsystems/playout.md), [srt-ingest.md](../subsystems/srt-ingest.md), [scte35.md](../subsystems/scte35.md), [graphics-and-dve.md](../subsystems/graphics-and-dve.md), [stmap.md](../subsystems/stmap.md), [transition.md](../subsystems/transition.md), [control-plane.md](../subsystems/control-plane.md)
- **Operations:** [deployment.md](../operations/deployment.md)
- **Root:** [architecture.md](../architecture.md), [README.md](../README.md)
