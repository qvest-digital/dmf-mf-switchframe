# SCTE-35 Fixes

Compliance gaps and follow-up items identified by reviewing our implementation against ANSI/SCTE 35 2023r1.

## Non-Compliant (Spec Violations)

### 1. Cancel uses cue-in instead of `splice_event_cancel_indicator`

**Spec:** `splice_event_cancel_indicator=1` cancels a pending splice. No further fields follow.

**Ours:** `CancelEvent()` sends a full cue-in `splice_insert` with `out_of_network_indicator=0`. Semantically different — one cancels the event, the other ends the break normally. Spec-strict receivers may not treat them equivalently.

**Fix:** Add a `cancelSpliceInsert()` method that builds a `SpliceInsert` with `SpliceEventCancelIndicator: true` and no other fields. Use this in `CancelEvent()` instead of sending a cue-in.

### 2. No `segmentation_event_cancel_indicator` support

**Spec:** `segmentation_event_cancel_indicator=1` within a `segmentation_descriptor` cancels a previously signaled segmentation event by `segmentation_event_id`.

**Ours:** No `CancelSegmentationEvent()` method exists. No way to set this flag on the wire.

**Fix:** Add `CancelSegmentationEvent(segEventID uint32)` to the injector, which sends a `time_signal` with a `segmentation_descriptor` that has `SegmentationEventCancelIndicator: true`.

## Significant Limitations (Spec-Valid but Limiting)

### 3. `time_signal` PTS always 0

**Spec:** `time_signal` carries a `splice_time()` with a meaningful PTS for downstream DAI systems.

**Ours:** `scte35lib.NewTimeSignal(0)` hardcodes `pts_time=0`. With `pts_adjustment=0`, the effective PTS is 0, which is nonsensical for downstream systems that need to know WHEN to splice.

**Fix:** Pass the current stream PTS (from `currentPTS()`) into `NewTimeSignal()`. For scheduled cues, compute `currentPTS + preRollTicks`.

### 4. `splice_insert` always immediate — PTS never on wire

**Spec:** `splice_insert` supports both `splice_immediate_flag=1` (splice ASAP) and PTS-based scheduling via `splice_time()`.

**Ours:** `SpliceImmediateFlag` is hardcoded to `true` in `Encode()`. Even when `ScheduleCue()` computes a PTS, it's stored internally but never encoded. DAI headends expecting a PTS-based splice point won't get one.

**Fix:** When `SpliceTimePTS` is set on the `CueMessage`, set `SpliceImmediateFlag=false` and populate `Program.SpliceTime.PTSTime` with the computed value.

### 5. Multiple UPIDs per descriptor not supported

**Spec:** Each `segmentation_descriptor` can carry multiple UPIDs. The MID type (0x0D) allows composite identifiers.

**Ours:** Only one UPID per descriptor on encode. Only the first UPID extracted on decode.

**Fix:** Change `SegmentationDescriptor.UPID` from single `[]byte` to a list of `{Type uint8, Data []byte}`. Update encode/decode to handle the full UPID list.

### 6. `unique_program_id` / `avail_num` / `avails_expected` always 0

**Spec:** These fields on `splice_insert` identify the program and avail position within a break. Both being 0 means "not applicable" (valid), but ad decisioning systems rely on them for avail management.

**Ours:** Never set. The fields don't exist on `CueMessage`.

**Fix:** Add `UniqueProgramID uint16`, `AvailNum uint8`, `AvailsExpected uint8` to `CueMessage`. Wire them through `Encode()` to the `SpliceInsert` struct. Expose in the REST API `scte35CueRequest`.

## Not Implemented (Optional, Not a Compliance Issue)

These are optional features we intentionally omit:

- **`splice_schedule` (0x04):** Rarely used; modern systems prefer `splice_insert` or `time_signal`.
- **`bandwidth_reservation` (0x07):** No-op command, rarely needed.
- **`private_command` (0xFF):** Vendor-specific.
- **`avail_descriptor` (0x00):** Legacy; superseded by `segmentation_descriptor`.
- **`DTMF_descriptor` (0x01):** Legacy analog tones.
- **`time_descriptor` (0x03):** TAI clock sync, not needed for injection.
- **`audio_descriptor` (0x04):** Audio component identification.
- **Encryption:** Not needed for local injection.
- **Component splice mode:** Virtually unused in modern systems.
- **Redundant transmission (3-5x):** Spec says SHOULD, not MUST. Acceptable for reliable local transport.
- **`segmentation_event_id_compliance_indicator`:** Added in 2020 revision, not critical.

## Dead Code / Unwired Features

### 7. Webhook dispatcher not wired

`webhook.go` exists with tests but is never instantiated or called from the `Injector`. The `InjectorConfig.WebhookURL` and `WebhookTimeoutMs` fields are stored but unused.

**Fix:** Add a `webhook *WebhookDispatcher` field to `Injector`. Call `Dispatch()` at each lifecycle event (inject, return, cancel, hold, extend).

### 8. Rules engine not evaluated during injection

The `Injector` has a `rules` field set via `SetRuleEngine()`, but `Evaluate()` is never called during `InjectCue()`. Rules are only evaluated externally.

**Fix:** Call `rules.Evaluate(msg, "")` inside `InjectCue()` before encoding. Apply the action (pass/delete/replace) before sending to the muxer sink.

### 9. Per-destination SCTE-35 stripping not functional

`DestinationConfig.SCTE35Enabled` field exists but no logic strips PID 0x102 from output when `SCTE35Enabled=false`.

**Fix:** In `rebuildAdaptersLocked()`, wrap adapters with a TS packet filter that drops SCTE-35 PID packets when `SCTE35Enabled=false`.

### 10. Unpopulated state fields

Several fields in `internal/types.go` are defined but never populated:
- `SCTE35State.PendingCues`
- `SCTE35DescriptorInfo.SegmentationTypeName` / `UPIDTypeName`
- `SCTE35Event.Descriptors` / `SpliceTimePTS` / `Source` / `DestinationID` / `AvailNum` / `AvailsExpected`
- `SCTE35DescriptorInfo.Cancelled`

**Fix:** Either populate them in the state enrichment functions (`app_state.go`) or remove the fields to avoid confusion.
