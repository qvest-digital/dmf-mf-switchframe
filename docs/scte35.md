# SCTE-35 Ad Insertion

## Overview

SCTE-35 (ANSI/SCTE 35) is the standard for splice signaling in MPEG-TS
streams, used by cable headends, OTT platforms, and SSAI systems to mark
ad insertion points. SwitchFrame implements real-time SCTE-35 injection
into its MPEG-TS output, covering the two most common command types:

- **splice_insert** (command_type 0x05) -- the primary ad break signal.
  Immediate or PTS-scheduled, with optional auto-return timers.
- **time_signal** (command_type 0x06) -- carries segmentation descriptors
  for content identification, placement opportunities, and program
  boundaries.

Additional capabilities:

- Signal conditioning rules engine (first-match-wins, AND/OR compound
  conditions) for filtering, modifying, or dropping cues before mux.
- Per-destination SCTE-35 enable/disable for SRT outputs.
- Webhook notifications for external integrations.
- splice_null heartbeat to keep downstream decoders alive.
- 5 macro actions for automated workflows.
- Keyboard shortcuts and a dedicated UI panel.

The implementation wraps [Comcast/scte35-go](https://github.com/Comcast/scte35-go)
v1.7.1 for binary encoding and decoding.

---

## Table of Contents

- [Quick Start](#quick-start)
- [CLI Flags](#cli-flags)
- [Concepts](#concepts)
  - [Splice Insert](#splice-insert)
  - [Time Signal](#time-signal)
  - [Timing Modes](#timing-modes)
  - [Auto-Return](#auto-return)
  - [Hold and Extend](#hold-and-extend)
  - [Cancel](#cancel)
  - [Heartbeat](#heartbeat)
- [API Reference](#api-reference)
  - [Cue Management](#cue-management)
  - [Rules Management](#rules-management)
- [Signal Conditioning Rules](#signal-conditioning-rules)
  - [How Rules Work](#how-rules-work)
  - [Operators](#operators)
  - [Available Fields](#available-fields)
  - [Preset Templates](#preset-templates)
  - [Rules Persistence](#rules-persistence)
- [Per-Destination Filtering](#per-destination-filtering)
- [MPEG-TS Integration](#mpeg-ts-integration)
- [Webhooks](#webhooks)
- [Macro Actions](#macro-actions)
- [Keyboard Shortcuts](#keyboard-shortcuts)
- [UI Panel](#ui-panel)
- [State Broadcast](#state-broadcast)
- [Architecture](#architecture)
- [SCTE-104 Integration](#scte-104-integration)
- [Known Limitations](#known-limitations)

---

## Quick Start

Enable SCTE-35 with the `--scte35` flag:

```bash
./switchframe --scte35
```

Inject a 30-second ad break with auto-return:

```bash
curl -X POST https://localhost:8080/api/scte35/cue \
  -H "Content-Type: application/json" \
  -d '{
    "commandType": "splice_insert",
    "isOut": true,
    "durationMs": 30000,
    "autoReturn": true
  }'
```

The response includes the assigned event ID:

```json
{
  "eventId": 1,
  "state": { ... }
}
```

Return to program early (before auto-return fires):

```bash
curl -X POST https://localhost:8080/api/scte35/return
```

To return a specific event by ID:

```bash
curl -X POST https://localhost:8080/api/scte35/return/1
```

---

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--scte35` | `false` | Enable SCTE-35 support. All `/api/scte35/*` endpoints return 501 when disabled. |
| `--scte35-pid` | `0x102` | MPEG-TS PID for the SCTE-35 elementary stream. |
| `--scte35-preroll` | `4000` | Default pre-roll time in milliseconds for scheduled cues. |
| `--scte35-heartbeat` | `5000` | Interval in milliseconds between splice_null heartbeat messages. Set to `0` to disable. |
| `--scte35-verify` | `false` | Verify encoded SCTE-35 sections by round-trip decode (encode then decode back). Catches encoding errors at the cost of extra CPU. |
| `--scte35-webhook` | `""` | URL for async HTTP POST event notifications. Empty string disables webhooks. |
| `--scte104` | `false` | Enable bidirectional SCTE-104 translation on MXL data flows. Requires `--scte35` and MXL build (`-tags "cgo mxl"`). |

---

## Concepts

### Splice Insert

splice_insert (command_type 0x05) is the primary mechanism for signaling
ad breaks in SCTE-35. It supports both cue-out (enter break) and cue-in
(return to program) modes.

Cue request fields:

| Field | Type | Description |
|-------|------|-------------|
| `commandType` | string | `"splice_insert"` |
| `isOut` | bool | `true` for cue-out (start break), `false` for cue-in (return) |
| `durationMs` | int64 | Break duration in milliseconds. Optional. |
| `autoReturn` | bool | Automatically send cue-in when duration expires |
| `preRollMs` | int64 | Pre-roll for PTS-scheduled splice. Omit or `0` for immediate. |
| `eventId` | uint32 | Splice event ID. Auto-assigned if `0` or omitted. |
| `uniqueProgramId` | uint16 | Identifies the program within the avail |
| `availNum` | uint8 | Avail number within the group |
| `availsExpected` | uint8 | Total avails expected in the group |

### Time Signal

time_signal (command_type 0x06) carries segmentation descriptors for
content identification, placement opportunity signaling, and program
boundary marking.

Cue request fields:

| Field | Type | Description |
|-------|------|-------------|
| `commandType` | string | `"time_signal"` |
| `preRollMs` | int64 | Pre-roll for PTS-scheduled splice. Omit or `0` for immediate. |
| `descriptors` | array | Array of segmentation descriptor objects |

Each descriptor object:

| Field | Type | Description |
|-------|------|-------------|
| `segmentationType` | uint8 | Segmentation type ID (e.g., 0x22 for break start, 0x30 for provider placement) |
| `durationMs` | int64 | Segmentation duration in milliseconds. Optional. |
| `upidType` | uint8 | UPID type (e.g., 0x01 for user-defined, 0x09 for ADI) |
| `upid` | string | UPID value |

### Timing Modes

**Immediate.** When `preRollMs` is omitted or `0`, the splice command is
injected with `splice_immediate_flag` set. The downstream splicer acts
on it as soon as it receives it.

**Scheduled.** When `preRollMs > 0`, the injector computes a splice time
PTS from the current video PTS plus the pre-roll offset:

```
splice_time_PTS = current_video_PTS + (preRollMs * 90)
```

The 90 kHz multiplier converts milliseconds to MPEG-TS PTS ticks. The
current video PTS is obtained from `Switcher.LastBroadcastVideoPTS()` via
atomic load.

### Auto-Return

When `autoReturn` is `true` and a duration is specified, the injector
starts a wall-clock timer equal to the break duration. When the timer
fires, a cue-in (splice_insert with `isOut=false`) is automatically
injected for the corresponding event ID.

The auto-return timer can be paused with hold or reset with extend.

### Hold and Extend

**Hold** pauses the auto-return timer indefinitely. Use this when a break
needs to run longer than originally planned (e.g., a commercial pod is
running late). The break remains active until an explicit return or cancel.

```bash
curl -X POST https://localhost:8080/api/scte35/hold/1
```

**Extend** sets a new total duration for the break and resets the
auto-return timer to fire at the new expiration time. Remaining time is
calculated from the original start time. If the new duration has already
elapsed, the timer is not started (manual return required). Extend
also clears the held state.

```bash
curl -X POST https://localhost:8080/api/scte35/extend/1 \
  -H "Content-Type: application/json" \
  -d '{"durationMs": 60000}'
```

The extend operation also sends an updated splice_insert with the new
break duration to downstream decoders.

### Cancel

**splice_event_cancel_indicator** cancels a pending or active splice_insert
event by event ID. The cancel message is encoded and injected into the TS
stream, and the event is removed from active tracking.

```bash
curl -X POST https://localhost:8080/api/scte35/cancel/1
```

**segmentation_event_cancel_indicator** cancels a segmentation event. This
sends a time_signal with a segmentation descriptor that has the cancel
indicator set. Unlike splice_insert cancel, this does not require the event
to be actively tracked.

```bash
curl -X POST https://localhost:8080/api/scte35/cancel-segmentation/42
```

### Heartbeat

When `--scte35-heartbeat` is set to a positive value, the injector runs a
background goroutine that sends splice_null (command_type 0x00) messages at
the configured interval. splice_null carries no operational data but keeps
downstream SCTE-35 decoders alive and confirms the SCTE-35 PID is active.

Set `--scte35-heartbeat 0` to disable.

---

## API Reference

All endpoints require `--scte35` to be enabled. When disabled, every
`/api/scte35/*` endpoint returns `501 Not Implemented`.

Mutating endpoints (POST, PUT, DELETE) are subject to operator middleware
when multi-operator mode is active.

### Cue Management

#### POST /api/scte35/cue

Inject a SCTE-35 cue. Supports both splice_insert and time_signal.

**Request body:**

```json
{
  "commandType": "splice_insert",
  "isOut": true,
  "durationMs": 30000,
  "autoReturn": true,
  "preRollMs": 4000,
  "eventId": 0,
  "uniqueProgramId": 1,
  "availNum": 1,
  "availsExpected": 1,
  "descriptors": []
}
```

When `preRollMs > 0`, the cue is PTS-scheduled. Otherwise it is injected
immediately.

When `eventId` is `0` or omitted for splice_insert commands, an ID is
auto-assigned from an incrementing counter.

**Response:**

```json
{
  "eventId": 1,
  "state": { ... }
}
```

#### POST /api/scte35/return

Return the most recent active event to program (cue-in).

**Response:** ControlRoomState JSON.

#### POST /api/scte35/return/{eventId}

Return a specific active event to program by event ID.

**Response:** ControlRoomState JSON.

#### POST /api/scte35/cancel/{eventId}

Cancel a splice_insert event. Sends a splice_insert with
`splice_event_cancel_indicator` set and removes the event from active
tracking.

**Response:** ControlRoomState JSON.

#### POST /api/scte35/cancel-segmentation/{segEventId}

Cancel a segmentation event. Sends a time_signal with a segmentation
descriptor that has `segmentation_event_cancel_indicator` set. Does not
require the event to be actively tracked.

**Response:** ControlRoomState JSON.

#### POST /api/scte35/hold/{eventId}

Hold an active event's auto-return timer indefinitely.

**Response:** ControlRoomState JSON.

#### POST /api/scte35/extend/{eventId}

Extend an active event's duration and reset the auto-return timer.

**Request body:**

```json
{
  "durationMs": 60000
}
```

`durationMs` is required and must be positive.

**Response:** ControlRoomState JSON.

#### GET /api/scte35/status

Returns the current injector state snapshot.

**Response:**

```json
{
  "enabled": true,
  "activeEvents": {
    "1": {
      "eventId": 1,
      "commandType": "splice_insert",
      "isOut": true,
      "durationMs": 30000,
      "elapsedMs": 12345,
      "remainingMs": 17655,
      "autoReturn": true,
      "held": false,
      "spliceTimePts": 0,
      "startedAt": 1741500000000
    }
  },
  "eventLog": [ ... ],
  "heartbeatOk": true
}
```

#### GET /api/scte35/log

Returns the event log (circular buffer, max 256 entries by default).

**Response:** Array of `EventLogEntry` objects.

#### GET /api/scte35/active

Returns a sorted array of active event IDs.

**Response:**

```json
[1, 2, 5]
```

### Rules Management

#### GET /api/scte35/rules

List all signal conditioning rules.

**Response:** Array of `Rule` objects.

#### POST /api/scte35/rules

Create a new rule. An 8-character hex ID is auto-assigned.

**Request body:**

```json
{
  "name": "Strip short avails",
  "enabled": true,
  "conditions": [
    {"field": "command_type", "operator": "=", "value": "5"},
    {"field": "duration", "operator": "<", "value": "15000"}
  ],
  "logic": "and",
  "action": "delete"
}
```

**Response:** The created `Rule` object with `id` assigned.

#### PUT /api/scte35/rules/{id}

Update an existing rule by ID. The `id` field in the request body is
ignored; the path parameter takes precedence.

**Request body:** Same as POST.

**Response:** ControlRoomState JSON.

#### DELETE /api/scte35/rules/{id}

Delete a rule by ID.

**Response:** ControlRoomState JSON.

#### PUT /api/scte35/rules/default

Set the default action applied when no rule matches.

**Request body:**

```json
{
  "action": "pass"
}
```

Valid actions: `"pass"`, `"delete"`, `"replace"`.

**Response:** ControlRoomState JSON.

#### POST /api/scte35/rules/reorder

Reorder rules. All current rule IDs must be present exactly once.

**Request body:**

```json
{
  "ids": ["a1b2c3d4", "e5f6a7b8", "c9d0e1f2"]
}
```

**Response:** ControlRoomState JSON.

#### GET /api/scte35/rules/templates

List the 5 built-in rule templates.

**Response:** Array of `Rule` objects (with empty IDs and `enabled: false`).

#### POST /api/scte35/rules/from-template

Create a rule from a named template. The created rule starts disabled.

**Request body:**

```json
{
  "name": "Strip short avails"
}
```

**Response:** The created `Rule` object.

---

## Signal Conditioning Rules

### How Rules Work

The rule engine evaluates every injected cue against an ordered list of
rules. Evaluation stops at the first matching rule (first-match-wins).
If no rule matches, the default action applies (default: `"pass"`).

Each rule has:

- **Conditions** -- one or more field/operator/value predicates.
- **Logic** -- `"and"` (all conditions must match) or `"or"` (any
  condition must match). Defaults to `"and"`.
- **Action** -- what to do when the rule matches:
  - `"pass"` -- inject the cue unchanged.
  - `"delete"` -- silently drop the cue.
  - `"replace"` -- modify the cue using `replaceWith` parameters before
    injecting.
- **Destinations** -- optional list of destination IDs. When set, the rule
  only applies to cues destined for those specific outputs. When empty,
  the rule applies to all destinations.
- **Enabled** -- disabled rules are skipped during evaluation.

### Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `=` | Equals | `command_type = 5` |
| `!=` | Not equals | `command_type != 0` |
| `>` | Greater than | `duration > 15000` |
| `<` | Less than | `duration < 15000` |
| `>=` | Greater or equal | `duration >= 30000` |
| `<=` | Less or equal | `duration <= 60000` |
| `contains` | String contains | `upid contains example.com` |
| `range` | Inclusive numeric range | `segmentation_type_id range 52-55` |
| `matches` | Regex match | `upid matches ^https://` |

Numeric comparisons parse both the field value and the condition value as
floats. If either fails to parse, the comparison falls back to
lexicographic string ordering.

### Available Fields

| Field | Type | Description |
|-------|------|-------------|
| `command_type` | int | SCTE-35 command type. `0` = splice_null, `5` = splice_insert, `6` = time_signal. |
| `is_out` | string | Out-of-network indicator. `"true"` or `"false"`. |
| `duration` | int | Break duration in milliseconds. For splice_insert, taken from `BreakDuration`. For time_signal, taken from the first descriptor's duration ticks (converted to ms). Returns `"0"` when absent. |
| `event_id` | int | Splice event ID. |
| `segmentation_type_id` | int | Segmentation type from the first descriptor. Returns `"0"` when no descriptors are present. |
| `upid` | string | UPID string from the first descriptor. Returns `""` when no descriptors are present. |

### Preset Templates

5 built-in templates are available via `GET /api/scte35/rules/templates` and
can be instantiated with `POST /api/scte35/rules/from-template`:

| Template Name | Action | Description |
|---------------|--------|-------------|
| Strip short avails | delete | Drop splice_insert commands with duration < 15 seconds |
| Strip program boundaries | delete | Drop segmentation types 16-17 (program start/end) |
| Fix missing duration | replace | Replace duration=0 with 30 seconds |
| Pass placement opportunities | pass | Explicitly pass segmentation types 52-55 (provider/distributor placement opportunities) |
| Block non-SSAI signals | delete | Drop all non-splice_null commands |

Templates are created in disabled state. Enable them after review.

### Rules Persistence

Rules are stored at `~/.switchframe/scte35_rules.json`. The file uses
atomic writes (temp file + fsync + rename) to prevent corruption on crash.
The on-disk format:

```json
{
  "rules": [ ... ],
  "defaultAction": "pass"
}
```

The internal `RuleEngine` is rebuilt from the store contents after every
mutation (create, update, delete, reorder, set default). Only enabled
rules are loaded into the engine.

---

## Per-Destination Filtering

Each SRT destination has a `scte35Enabled` flag (set via the destination
CRUD API at `/api/output/destinations`). When `false`, SCTE-35 PID
packets are stripped from the MPEG-TS output before delivery to that
destination.

This allows different downstream consumers to receive or not receive
SCTE-35 signals from the same program output. For example, a direct-to-air
feed might carry SCTE-35 while a social media simulcast does not.

---

## MPEG-TS Integration

SCTE-35 data is carried in the MPEG-TS output as follows:

- **PID**: 0x102 by default, configurable via `--scte35-pid`.
- **PMT registration**: stream_type 0x86 with a CUEI registration
  descriptor (format_identifier `0x43554549`, the ASCII encoding of
  `CUEI`).
- **Framing**: PSI section framing, not PES. SCTE-35 splice_info_sections
  are written directly as PSI sections with pointer fields.
- **Continuity counter**: Maintained per PID, incrementing with each TS
  packet.
- **Pre-init buffering**: SCTE-35 sections written before the first
  keyframe are queued internally and flushed after the muxer initializes
  (PAT/PMT written, first keyframe received).
- **Late-join**: When an SRT client connects during an active break,
  `SyntheticBreakState()` generates a splice_insert with the remaining
  break duration so the client can pick up the in-progress event.

---

## Webhooks

When `--scte35-webhook URL` is configured, the injector sends HTTP POST
notifications for lifecycle events. The webhook dispatches asynchronously
and never blocks the injection path.

### Event Types

| Type | Trigger |
|------|---------|
| `cue_out` | Cue-out injected (splice_insert with isOut=true) |
| `cue_in` | Cue-in injected (return to program) |
| `cancel` | splice_event_cancel_indicator sent |
| `cancel_segmentation` | segmentation_event_cancel_indicator sent |
| `hold` | Auto-return timer held |
| `extend` | Break duration extended |

### Payload Format

```json
{
  "type": "cue_out",
  "eventId": 1,
  "command": "splice_insert",
  "isOut": true,
  "durationMs": 30000,
  "remainingMs": 0,
  "timestamp": 1741500000000,
  "pts": 0
}
```

### Error Handling

The webhook dispatcher is fire-and-forget. HTTP errors and connection
failures are logged but do not affect cue injection. There is no retry
mechanism.

---

## Macro Actions

5 SCTE-35 macro actions are available for automated workflows. These can
be used in macro step sequences alongside other actions (cut, preview,
transition, wait, etc.).

| Action | Params | Description |
|--------|--------|-------------|
| `scte35_cue` | `commandType`, `isOut`, `durationMs`, `autoReturn` | Inject a cue. Params are passed as a map to the injector. |
| `scte35_return` | `eventId` (optional; `0` or omitted returns most recent) | Return to program |
| `scte35_cancel` | `eventId` (required) | Cancel a splice event |
| `scte35_hold` | `eventId` (required) | Hold auto-return |
| `scte35_extend` | `eventId` (required), `durationMs` (required) | Extend break duration |

Example macro definition (JSON):

```json
{
  "name": "30s Ad Break",
  "steps": [
    {
      "action": "scte35_cue",
      "params": {
        "commandType": "splice_insert",
        "isOut": true,
        "durationMs": 30000,
        "autoReturn": true
      }
    }
  ]
}
```

---

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Shift+B` | Start a 30-second ad break with auto-return |
| `Shift+R` | Return to program (most recent active event) |
| `Shift+H` | Hold the current break |
| `Shift+E` | Extend the current break by 30 seconds |

---

## UI Panel

The SCTE-35 panel is the 7th tab in BottomTabs, accessible via
`Ctrl+Shift+7`. It has a three-zone layout:

1. **Quick Actions** -- Duration presets (15s, 30s, 60s, 90s, 120s),
   auto-return toggle, pre-roll selector, AD BREAK and RETURN buttons.
   Active events appear as cards with countdown timers and hold/extend/cancel
   controls.

2. **Cue Builder** -- Advanced form with separate splice_insert and
   time_signal tabs. splice_insert exposes all fields (event ID, duration,
   avail, unique program ID). time_signal exposes segmentation descriptor
   fields (type, UPID type, UPID value, duration). SEND CUE button submits
   the built cue.

3. **Event Log** -- Reverse-chronological history of all SCTE-35 events
   with status badges (injected, returned, cancelled, held, extended).
   Clickable items open a detail flyout with full event metadata.

The component is implemented in `ui/src/components/SCTE35Panel.svelte`.

---

## State Broadcast

SCTE-35 state is included in `ControlRoomState.scte35`, pushed to browsers
via the MoQ control track alongside all other switcher state.

```typescript
interface SCTE35State {
  enabled: boolean;
  scte104Enabled?: boolean;
  activeEvents: Record<number, {
    eventId: number;
    commandType: string;
    isOut: boolean;
    durationMs?: number;
    elapsedMs: number;
    remainingMs?: number;
    autoReturn: boolean;
    held: boolean;
    spliceTimePts: number;
    startedAt: number;
    descriptors?: SegmentationDescriptor[];
  }>;
  eventLog: EventLogEntry[];
  heartbeatOk: boolean;
  config: {
    heartbeatIntervalMs: number;
    defaultPreRollMs: number;
    pid: number;
    verifyEncoding: boolean;
    webhookUrl: string;
  };
}
```

The `config` block is included so the UI panel can display current
settings without a separate API call.

---

## Architecture

### Package Structure

```
server/scte35/
  message.go         CueMessage types wrapping Comcast/scte35-go (encode/decode)
  injector.go        Core lifecycle: inject, schedule, auto-return, hold, extend, heartbeat
  parser.go          Pass-through TS parser with CRC validation, PID detection
  rules.go           Signal conditioning rules engine (first-match-wins, AND/OR)
  rules_store.go     File-based rules CRUD with preset templates
  webhook.go         Async webhook dispatcher

server/scte104/
  message.go         SCTE-104 message types (Message, Operation, SpliceRequestData, etc.)
  decode.go          Binary decoder for SOM and MOM (Multiple Operation Message)
  encode.go          Binary encoder (always outputs MOM format)
  st291.go           SMPTE ST 291 VANC wrapper: ParseST291() / WrapST291() (DID=0x41, SDID=0x07)
  translate.go       Bidirectional translation: ToCueMessage() and FromCueMessage()

server/control/
  api_scte35.go      17 REST API handlers (+ 1 cancel-segmentation = 18 total)

server/output/
  muxer.go           TSMuxer SCTE-35 PID integration (PSI section framing)

server/macro/
  types.go           5 SCTE-35 macro action constants
  runner.go          Macro runner SCTE-35 action dispatch

ui/src/components/
  SCTE35Panel.svelte Frontend panel (quick actions + cue builder + event log)
```

### Data Flow

```
API request (POST /api/scte35/cue)
  -> control/api_scte35.go: parse JSON, build CueMessage
  -> scte35/injector.go: evaluate rules, encode, inject
    -> rules engine (optional): first-match-wins evaluation
    -> CueMessage.Encode(): scte35-go binary encoding
    -> muxerSink callback: encoded bytes -> TSMuxer
      -> output/muxer.go: PSI section framing, PID 0x102
      -> TS packets written to recorder + SRT destinations
    -> active event tracking (splice_insert cue-out only)
    -> auto-return timer (if autoReturn + duration)
    -> event log (circular buffer)
    -> state change callback -> ControlRoomState broadcast
    -> webhook dispatch (async, if configured)
```

### PTS Synchronization

The injector obtains the current video PTS via the `ptsFn` callback,
which calls `Switcher.LastBroadcastVideoPTS()`. This value is an atomic
load of the most recently broadcast video frame's PTS in 90 kHz ticks.
Scheduled cues add the pre-roll offset to this PTS to compute the splice
time.

### Concurrency

The `Injector` uses a single `sync.Mutex` protecting the active events
map, event log, and rule engine pointer. The state change callback and
webhook dispatch are invoked outside the lock to prevent deadlock (the
callback may call `State()` which acquires the same mutex).

The heartbeat goroutine runs independently and only acquires the lock
indirectly through `sendHeartbeat()` (which does not acquire the lock --
it only calls `muxerSink`).

---

## SCTE-104 Integration

SCTE-104 is the automation-to-splicer protocol used by broadcast
automation systems (typically over TCP/serial or embedded in SDI ancillary
data). SwitchFrame supports bidirectional translation between SCTE-104
messages carried on MXL data flows and SCTE-35 cues in the MPEG-TS output.

### Prerequisites

- `--scte35` must be enabled
- `--scte104` flag must be set
- Built with MXL support (`-tags "cgo mxl"`)
- MXL source spec must include a data flow UUID: `videoUUID:audioUUID:dataUUID`

### Inbound Path (MXL → SCTE-35)

```
MXL data grain (ancillary data from SDI VANC)
  → scte104.ParseST291(data)       // strip ST 291 wrapper, validate DID=0x41 SDID=0x07
  → scte104.Decode(payload)         // parse SOM or MOM binary → *scte104.Message
  → scte104.ToCueMessage(msg)       // translate to *scte35.CueMessage (Source="scte104")
  → scte35.Injector.InjectCue()     // inject into MPEG-TS output (rules engine applies)
```

### Outbound Path (SCTE-35 → MXL)

```
scte35.Injector SCTE104Sink callback (fires on every injection)
  → scte104.FromCueMessage(cue)     // translate *scte35.CueMessage → *scte104.Message
  → scte104.Encode(msg)             // serialize to SCTE-104 binary (always MOM format)
  → scte104.WrapST291(data)         // wrap in ST 291 VANC packet (DID=0x41, SDID=0x07)
  → mxl.Writer.WriteDataGrain()     // write to MXL shared memory
```

### Translation Rules

**SCTE-104 → SCTE-35 (inbound):**

| SCTE-104 Operation | SCTE-35 Command |
|---------------------|-----------------|
| splice_request (0x0101), start normal/immediate | splice_insert, isOut=true |
| splice_request, end normal/immediate | splice_insert, isOut=false |
| splice_request, cancel | splice_insert with cancel indicator |
| splice_null (0x0102) | splice_null |
| time_signal_request (0x0104) | time_signal |
| segmentation_descriptor_request (0x010B) | time_signal with segmentation descriptor |

**SCTE-35 → SCTE-104 (outbound):**

| SCTE-35 Command | SCTE-104 Operation |
|-----------------|---------------------|
| splice_null | splice_null |
| splice_insert, isOut=true | splice_request, start immediate |
| splice_insert, isOut=false | splice_request, end immediate |
| splice_insert, cancel | splice_request, cancel |
| time_signal | time_signal_request + segmentation_descriptor_request per descriptor |

### Example

```bash
./bin/switchframe \
  --scte35 \
  --scte104 \
  --mxl-sources "VIDEO_UUID:AUDIO_UUID:DATA_UUID" \
  --mxl-output program
```

### Limitations

- Fragmented ST 291 packets (`continued_pkt` or `following_pkt` set) are
  not supported and return `ErrST291Fragmented`. Maximum single-packet
  payload is 253 bytes.
- `SegNum`/`SegExpected` from SCTE-104 segmentation descriptors are not
  carried through to SCTE-35 (lost in Comcast/scte35-go translation).

---

## Known Limitations

- **Single UPID per descriptor.** Multiple UPIDs per segmentation
  descriptor are not supported. Only the first UPID is extracted on
  decode, and only one can be specified on encode.
- **No splice_schedule (0x04).** splice_schedule is rarely used in modern
  systems and is not implemented.
- **No redundant transmission.** The SCTE-35 spec recommends (SHOULD, not
  MUST) redundant transmission of splice commands. SwitchFrame sends each
  command once.
- **No encryption.** SCTE-35 section encryption is not supported.
- **No webhook retry.** The webhook dispatcher is fire-and-forget. Failed
  requests are logged but not retried.
- **Regex compilation per evaluation.** The `matches` operator compiles
  the regex pattern on every rule evaluation. For high-frequency cue
  injection with regex rules, this adds overhead. Consider using
  `contains` or `=` operators when possible.
- **Library decoder bug workaround.** Comcast/scte35-go reads
  `unique_program_id`, `avail_num`, and `avails_expected` outside the
  `splice_event_cancel_indicator` guard, causing decode failures on
  spec-compliant cancel messages. SwitchFrame includes a fallback binary
  parser (`decodeSpliceInsertCancel`) and skips verification for cancel
  messages.
