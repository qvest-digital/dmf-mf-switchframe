//go:build correctness

package webtransport

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// Test Helpers
// ==========================================================================

// stateCapture collects state change callbacks from the switcher.
type stateCapture struct {
	mu     sync.Mutex
	states []internal.ControlRoomState
}

func (sc *stateCapture) callback(state internal.ControlRoomState) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.states = append(sc.states, state)
}

func (sc *stateCapture) count() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.states)
}

func (sc *stateCapture) last() internal.ControlRoomState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.states[len(sc.states)-1]
}

func (sc *stateCapture) all() []internal.ControlRoomState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	out := make([]internal.ControlRoomState, len(sc.states))
	copy(out, sc.states)
	return out
}

// waitForStates waits until at least n states have been captured or timeout.
func (sc *stateCapture) waitForStates(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sc.count() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d state broadcasts, got %d", n, sc.count())
}

// newSwitcherWithRawSources creates a Switcher with N raw sources (like MXL/SRT).
// Returns the switcher and the source keys.
func newSwitcherWithRawSources(t *testing.T, keys ...string) *switcher.Switcher {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	for _, key := range keys {
		sw.RegisterMXLSource(key)
	}
	t.Cleanup(func() { sw.Close() })
	return sw
}

// countUserSources returns the sources with IsVirtual=false, excluding the
// always-registered built-ins (source:black, source:colorbars).
func countUserSources(sources map[string]internal.SourceInfo) int {
	n := 0
	for _, s := range sources {
		if !s.IsVirtual {
			n++
		}
	}
	return n
}

// ==========================================================================
// TestStateBroadcastOnCut
//
// Verifies that Cut operations produce correct state broadcasts with
// accurate ProgramSource, PreviewSource, and TallyState fields.
// Asserts exact broadcast counts and verifies every source's tally state.
// ==========================================================================
func TestStateBroadcastOnCut(t *testing.T) {
	sw := newSwitcherWithRawSources(t, "camA", "camB", "camC")

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Cut to camA — first cut, no previous program
	err := sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}

	cap.waitForStates(t, 1, 2*time.Second)

	// Assert exactly 1 broadcast occurred (not >=1, exactly 1)
	if got := cap.count(); got != 1 {
		t.Fatalf("expected exactly 1 broadcast after first Cut, got %d", got)
	}

	state1 := cap.last()
	t.Logf("MEASURE: after Cut(camA): program=%s, preview=%s, tally=%v, sources=%d, seq=%d",
		state1.ProgramSource, state1.PreviewSource, state1.TallyState,
		len(state1.Sources), state1.Seq)

	// Verify programSource
	if state1.ProgramSource != "camA" {
		t.Errorf("expected ProgramSource=camA, got %q", state1.ProgramSource)
	}

	// Verify no preview (first cut, nothing was on program before)
	if state1.PreviewSource != "" {
		t.Errorf("expected PreviewSource=\"\" after first cut, got %q", state1.PreviewSource)
	}

	// Verify tally for ALL sources, not just the ones we cut to
	if state1.TallyState["camA"] != "program" {
		t.Errorf("expected tally[camA]=program, got %q", state1.TallyState["camA"])
	}
	if state1.TallyState["camB"] != "idle" {
		t.Errorf("expected tally[camB]=idle, got %q", state1.TallyState["camB"])
	}
	if state1.TallyState["camC"] != "idle" {
		t.Errorf("expected tally[camC]=idle, got %q", state1.TallyState["camC"])
	}

	// Verify Sources map contains all registered sources
	if countUserSources(state1.Sources) != 3 {
		t.Errorf("expected 3 user sources in broadcast, got %d: %v", countUserSources(state1.Sources), sourceKeys(state1.Sources))
	}
	for _, key := range []string{"camA", "camB", "camC"} {
		if _, ok := state1.Sources[key]; !ok {
			t.Errorf("source %q missing from Sources map", key)
		}
	}

	// Verify TransitionType is "cut" (not in transition)
	if state1.TransitionType != "cut" {
		t.Errorf("expected TransitionType=cut, got %q", state1.TransitionType)
	}
	if state1.InTransition {
		t.Errorf("expected InTransition=false after cut")
	}

	// Cut to camB — camA should move to preview
	err = sw.Cut(context.Background(), "camB")
	if err != nil {
		t.Fatalf("Cut(camB) failed: %v", err)
	}

	cap.waitForStates(t, 2, 2*time.Second)

	// Assert exactly 2 broadcasts total
	if got := cap.count(); got != 2 {
		t.Fatalf("expected exactly 2 broadcasts after second Cut, got %d", got)
	}

	state2 := cap.last()
	t.Logf("MEASURE: after Cut(camB): program=%s, preview=%s, tally=%v, seq=%d",
		state2.ProgramSource, state2.PreviewSource, state2.TallyState, state2.Seq)

	// Verify programSource changed
	if state2.ProgramSource != "camB" {
		t.Errorf("expected ProgramSource=camB, got %q", state2.ProgramSource)
	}

	// Verify preview: camA (old program) should move to preview
	if state2.PreviewSource != "camA" {
		t.Errorf("expected PreviewSource=camA, got %q", state2.PreviewSource)
	}

	// Verify tally for ALL three sources
	if state2.TallyState["camB"] != "program" {
		t.Errorf("expected tally[camB]=program, got %q", state2.TallyState["camB"])
	}
	if state2.TallyState["camA"] != "preview" {
		t.Errorf("expected tally[camA]=preview, got %q", state2.TallyState["camA"])
	}
	if state2.TallyState["camC"] != "idle" {
		t.Errorf("expected tally[camC]=idle, got %q", state2.TallyState["camC"])
	}

	// Verify seq incremented exactly by 1
	if state2.Seq != state1.Seq+1 {
		t.Errorf("expected Seq=%d (previous+1), got %d", state1.Seq+1, state2.Seq)
	}

	// Cut to camC — camB should move to preview
	err = sw.Cut(context.Background(), "camC")
	if err != nil {
		t.Fatalf("Cut(camC) failed: %v", err)
	}

	cap.waitForStates(t, 3, 2*time.Second)

	// Assert exactly 3 broadcasts total
	if got := cap.count(); got != 3 {
		t.Fatalf("expected exactly 3 broadcasts after third Cut, got %d", got)
	}

	state3 := cap.last()
	t.Logf("MEASURE: after Cut(camC): program=%s, preview=%s, tally=%v, seq=%d",
		state3.ProgramSource, state3.PreviewSource, state3.TallyState, state3.Seq)

	if state3.ProgramSource != "camC" {
		t.Errorf("expected ProgramSource=camC, got %q", state3.ProgramSource)
	}
	if state3.PreviewSource != "camB" {
		t.Errorf("expected PreviewSource=camB, got %q", state3.PreviewSource)
	}
	if state3.TallyState["camC"] != "program" {
		t.Errorf("expected tally[camC]=program, got %q", state3.TallyState["camC"])
	}
	if state3.TallyState["camB"] != "preview" {
		t.Errorf("expected tally[camB]=preview, got %q", state3.TallyState["camB"])
	}
	if state3.TallyState["camA"] != "idle" {
		t.Errorf("expected tally[camA]=idle, got %q", state3.TallyState["camA"])
	}

	// Verify seq incremented exactly by 1 from state2
	if state3.Seq != state2.Seq+1 {
		t.Errorf("expected Seq=%d (previous+1), got %d", state2.Seq+1, state3.Seq)
	}

	// Cutting to same source should NOT produce a broadcast
	err = sw.Cut(context.Background(), "camC")
	if err != nil {
		t.Fatalf("Cut(camC) again failed: %v", err)
	}

	// Small sleep to allow any rogue broadcast to arrive
	time.Sleep(50 * time.Millisecond)
	if got := cap.count(); got != 3 {
		t.Errorf("cutting to same source should not broadcast; expected 3 total, got %d", got)
	}
}

// ==========================================================================
// TestStateBroadcastOnSourceAdd
//
// Verifies that registering a new source triggers a state broadcast
// containing the new source in the Sources map.
// ==========================================================================
func TestStateBroadcastOnSourceAdd(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	t.Cleanup(func() { sw.Close() })

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Register a source — this should trigger a state broadcast
	sw.RegisterMXLSource("newCam")

	cap.waitForStates(t, 1, 2*time.Second)
	state := cap.last()

	t.Logf("MEASURE: sources=%v", sourceKeys(state.Sources))

	// Verify the new source is in the Sources map
	info, ok := state.Sources["newCam"]
	if !ok {
		t.Fatalf("expected source 'newCam' in Sources map, got keys: %v", sourceKeys(state.Sources))
	}

	// Verify the source has the correct key
	if info.Key != "newCam" {
		t.Errorf("expected source Key=newCam, got %q", info.Key)
	}

	// MXL sources get uppercase label
	t.Logf("MEASURE: source label=%q, position=%d", info.Label, info.Position)
	if info.Label != "NEWCAM" {
		t.Errorf("expected MXL source label=NEWCAM, got %q", info.Label)
	}

	// Register a second source and verify it appears
	countBefore := cap.count()
	sw.RegisterMXLSource("cam2")

	cap.waitForStates(t, countBefore+1, 2*time.Second)
	state2 := cap.last()

	t.Logf("MEASURE: sources after second add=%v", sourceKeys(state2.Sources))

	if _, ok := state2.Sources["cam2"]; !ok {
		t.Errorf("expected source 'cam2' in Sources map, got keys: %v", sourceKeys(state2.Sources))
	}

	// Both user-registered sources should be present.
	if countUserSources(state2.Sources) != 2 {
		t.Errorf("expected 2 user sources, got %d: %v", countUserSources(state2.Sources), sourceKeys(state2.Sources))
	}
}

// ==========================================================================
// TestStateBroadcastSerialization
//
// Verifies JSON round-trip of a REAL broadcast state produced by an actual
// Cut operation. This tests that buildStateLocked populates all fields
// correctly, not just that JSON works on a hand-crafted struct.
// Also checks Go↔TypeScript field name alignment.
// ==========================================================================
func TestStateBroadcastSerialization(t *testing.T) {
	// =====================================================================
	// Part 1: Capture a REAL broadcast state from actual operations
	// =====================================================================
	sw := newSwitcherWithRawSources(t, "camA", "camB")

	// Attach a mock mixer so audio fields are populated
	mock := &mockAudioStateProvider{
		programPeak:    [2]float64{-8.5, -10.2},
		masterLevel:    -3.0,
		gainReduction:  1.5,
		limThreshold:   -1.0,
		limCeiling:     -0.5,
		limGR:          0.3,
		momentaryLUFS:  -23.0,
		shortTermLUFS:  -24.0,
		integratedLUFS: -24.5,
		channels: map[string]internal.AudioChannel{
			"camA": {Level: -6.0, Trim: 0.0, Muted: false, AFV: true, PeakL: -12.0, PeakR: -14.0},
			"camB": {Level: -10.0, Trim: -2.0, Muted: true, AFV: false, PeakL: -20.0, PeakR: -22.0},
		},
	}
	sw.SetMixer(mock)

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Perform a real Cut to produce a real broadcast
	err := sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}
	cap.waitForStates(t, 1, 2*time.Second)

	// Cut to camB so we have preview + program
	err = sw.Cut(context.Background(), "camB")
	if err != nil {
		t.Fatalf("Cut(camB) failed: %v", err)
	}
	cap.waitForStates(t, 2, 2*time.Second)

	// Capture the ACTUAL broadcast state from the real code path
	realState := cap.last()
	t.Logf("MEASURE: real broadcast: program=%s, preview=%s, sources=%d, seq=%d",
		realState.ProgramSource, realState.PreviewSource, len(realState.Sources), realState.Seq)

	// Verify the real state has correct program/preview/tally
	if realState.ProgramSource != "camB" {
		t.Errorf("real broadcast ProgramSource: expected camB, got %q", realState.ProgramSource)
	}
	if realState.PreviewSource != "camA" {
		t.Errorf("real broadcast PreviewSource: expected camA, got %q", realState.PreviewSource)
	}

	// Verify real state has audio fields populated from the mock mixer
	if realState.ProgramPeak[0] != -8.5 || realState.ProgramPeak[1] != -10.2 {
		t.Errorf("real broadcast ProgramPeak: expected [-8.5, -10.2], got %v", realState.ProgramPeak)
	}
	if realState.MasterLevel != -3.0 {
		t.Errorf("real broadcast MasterLevel: expected -3.0, got %f", realState.MasterLevel)
	}
	if realState.GainReduction != 1.5 {
		t.Errorf("real broadcast GainReduction: expected 1.5, got %f", realState.GainReduction)
	}
	if realState.Limiter == nil {
		t.Fatal("real broadcast Limiter is nil — mixer wiring broken")
	} else {
		if realState.Limiter.Threshold != -1.0 {
			t.Errorf("real broadcast Limiter.Threshold: expected -1.0, got %f", realState.Limiter.Threshold)
		}
	}
	if realState.MomentaryLUFS != -23.0 {
		t.Errorf("real broadcast MomentaryLUFS: expected -23.0, got %f", realState.MomentaryLUFS)
	}
	if realState.AudioChannels == nil {
		t.Fatal("real broadcast AudioChannels is nil — mixer wiring broken")
	} else if ch, ok := realState.AudioChannels["camA"]; !ok {
		t.Error("real broadcast AudioChannels missing camA")
	} else if ch.Level != -6.0 {
		t.Errorf("real broadcast AudioChannels[camA].Level: expected -6.0, got %f", ch.Level)
	}

	// Verify pipeline format is populated (default format)
	if realState.PipelineFormat == nil {
		t.Error("real broadcast PipelineFormat is nil")
	} else {
		t.Logf("MEASURE: pipelineFormat: %dx%d %d/%d fps name=%s",
			realState.PipelineFormat.Width, realState.PipelineFormat.Height,
			realState.PipelineFormat.FPSNum, realState.PipelineFormat.FPSDen,
			realState.PipelineFormat.Name)
		if realState.PipelineFormat.Width == 0 || realState.PipelineFormat.Height == 0 {
			t.Error("real broadcast PipelineFormat has zero dimensions")
		}
	}

	// =====================================================================
	// Part 2: JSON round-trip the REAL state (not a hand-crafted one)
	// =====================================================================
	data, err := json.Marshal(realState)
	if err != nil {
		t.Fatalf("json.Marshal of real state failed: %v", err)
	}
	t.Logf("MEASURE: real state JSON size=%d bytes", len(data))

	var decoded internal.ControlRoomState
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("json.Unmarshal of real state failed: %v", err)
	}

	// Verify critical fields survive JSON round-trip
	if decoded.ProgramSource != realState.ProgramSource {
		t.Errorf("round-trip ProgramSource: %q vs %q", decoded.ProgramSource, realState.ProgramSource)
	}
	if decoded.PreviewSource != realState.PreviewSource {
		t.Errorf("round-trip PreviewSource: %q vs %q", decoded.PreviewSource, realState.PreviewSource)
	}
	if !reflect.DeepEqual(decoded.TallyState, realState.TallyState) {
		t.Errorf("round-trip TallyState: %v vs %v", decoded.TallyState, realState.TallyState)
	}
	if decoded.Seq != realState.Seq {
		t.Errorf("round-trip Seq: %d vs %d", decoded.Seq, realState.Seq)
	}
	if decoded.Timestamp != realState.Timestamp {
		t.Errorf("round-trip Timestamp: %d vs %d", decoded.Timestamp, realState.Timestamp)
	}
	if decoded.TransitionType != realState.TransitionType {
		t.Errorf("round-trip TransitionType: %q vs %q", decoded.TransitionType, realState.TransitionType)
	}
	if decoded.ProgramPeak != realState.ProgramPeak {
		t.Errorf("round-trip ProgramPeak: %v vs %v", decoded.ProgramPeak, realState.ProgramPeak)
	}
	if decoded.MasterLevel != realState.MasterLevel {
		t.Errorf("round-trip MasterLevel: %f vs %f", decoded.MasterLevel, realState.MasterLevel)
	}
	if len(decoded.Sources) != len(realState.Sources) {
		t.Errorf("round-trip Sources count: %d vs %d", len(decoded.Sources), len(realState.Sources))
	}
	// Verify audio channels survived
	if decoded.AudioChannels == nil {
		t.Fatal("round-trip AudioChannels is nil")
	}
	if ch, ok := decoded.AudioChannels["camA"]; !ok {
		t.Error("round-trip AudioChannels missing camA")
	} else if ch.Level != -6.0 {
		t.Errorf("round-trip AudioChannels[camA].Level: expected -6.0, got %f", ch.Level)
	}

	// =====================================================================
	// JSON field name verification against TypeScript expectations
	// =====================================================================
	// Parse the JSON into a raw map to inspect actual field names
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		t.Fatalf("unmarshal to raw map failed: %v", err)
	}

	// Field parity against the TypeScript ControlRoomState interface is
	// derived at test time from ui/src/lib/api/types.ts (see parseTSInterfaceFields
	// in ts_parser.go). Adding a field on either side is enough — no manifest
	// to maintain. If the TS file is moved or the interface renamed, the test
	// fails loudly with the path and name in the error.
	expectedTSFields, err := parseTSInterfaceFields(findRepoTypesTS(t), "ControlRoomState")
	if err != nil {
		t.Fatalf("parse TS ControlRoomState fields: %v", err)
	}

	// Check: every JSON field from Go should match a TS field
	goJSONKeys := make([]string, 0, len(rawMap))
	for k := range rawMap {
		goJSONKeys = append(goJSONKeys, k)
	}
	sort.Strings(goJSONKeys)
	t.Logf("MEASURE: Go JSON field names=%v", goJSONKeys)

	// Fields emitted by Go but not declared in TypeScript ControlRoomState
	var goOnlyFields []string
	for _, k := range goJSONKeys {
		if !expectedTSFields[k] {
			goOnlyFields = append(goOnlyFields, k)
		}
	}
	if len(goOnlyFields) > 0 {
		t.Errorf("Go emits JSON fields not declared in TypeScript ControlRoomState: %v", goOnlyFields)
	}

	// Fields declared in TypeScript but not emitted by Go (when fully populated)
	// Note: some may be legitimately absent due to omitempty on nil fields
	var tsOnlyFields []string
	for k := range expectedTSFields {
		if _, ok := rawMap[k]; !ok {
			tsOnlyFields = append(tsOnlyFields, k)
		}
	}
	if len(tsOnlyFields) > 0 {
		// These are fields the browser expects but Go didn't emit.
		// Some may be due to omitempty and nil values, which is fine.
		// But if a field is always expected, this is a bug.
		t.Logf("MEASURE: TS fields not emitted by Go (may be due to omitempty): %v", tsOnlyFields)
	}

	// Check Go ControlRoomState struct tags have ALL expected TS fields
	// by reflecting on the struct type
	stateType := reflect.TypeOf(internal.ControlRoomState{})
	goStructJSONTags := make(map[string]bool)
	for i := 0; i < stateType.NumField(); i++ {
		field := stateType.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Parse just the name (before any comma)
		name := strings.Split(tag, ",")[0]
		goStructJSONTags[name] = true
	}

	// Fields in TS but not in Go struct at all (genuine schema drift)
	var missingInGo []string
	for k := range expectedTSFields {
		if !goStructJSONTags[k] {
			missingInGo = append(missingInGo, k)
		}
	}
	if len(missingInGo) > 0 {
		t.Errorf("TypeScript declares fields not in Go ControlRoomState struct: %v", missingInGo)
	}

	// Fields in Go struct but not in TS interface (Go has extra fields)
	var missingInTS []string
	for k := range goStructJSONTags {
		if !expectedTSFields[k] {
			missingInTS = append(missingInTS, k)
		}
	}
	if len(missingInTS) > 0 {
		t.Errorf("Go ControlRoomState has fields not declared in TypeScript: %v", missingInTS)
	}

	// =====================================================================
	// Nested type field name checks: SourceInfo
	// =====================================================================
	// Go SourceInfo has keyConfig but TS SourceInfo does not
	sourceInfoType := reflect.TypeOf(internal.SourceInfo{})
	goSourceFields := extractJSONTags(sourceInfoType)
	tsSourceFields := map[string]bool{
		"key": true, "label": true, "type": true, "status": true,
		"position": true, "delayMs": true, "keyConfig": true,
		"isVirtual": true, "hasCaptions": true,
		"srt": true, "corrector": true,
	}

	var sourceMissingInTS []string
	for k := range goSourceFields {
		if !tsSourceFields[k] {
			sourceMissingInTS = append(sourceMissingInTS, k)
		}
	}
	if len(sourceMissingInTS) > 0 {
		t.Errorf("Go SourceInfo has fields not declared in TypeScript SourceInfo: %v", sourceMissingInTS)
	}

	// =====================================================================
	// Nested type field name checks: ReplayState
	// =====================================================================
	replayType := reflect.TypeOf(internal.ReplayState{})
	goReplayFields := extractJSONTags(replayType)
	tsReplayFields := map[string]bool{
		"state": true, "source": true, "speed": true, "loop": true,
		"position": true, "markIn": true, "markOut": true, "markSource": true,
		"buffers": true, "shutterAngle": true, "gpuAccelerated": true,
	}

	var replayMissingInTS []string
	for k := range goReplayFields {
		if !tsReplayFields[k] {
			replayMissingInTS = append(replayMissingInTS, k)
		}
	}
	if len(replayMissingInTS) > 0 {
		t.Errorf("Go ReplayState has fields not declared in TypeScript ReplayState: %v", replayMissingInTS)
	}

	// =====================================================================
	// Nested type field name checks: SCTE35State
	// =====================================================================
	scte35Type := reflect.TypeOf(internal.SCTE35State{})
	goSCTE35Fields := extractJSONTags(scte35Type)
	tsSCTE35Fields := map[string]bool{
		"enabled": true, "scte104Enabled": true, "activeEvents": true,
		"eventLog": true, "heartbeatOk": true, "config": true,
	}

	var scte35MissingInTS []string
	for k := range goSCTE35Fields {
		if !tsSCTE35Fields[k] {
			scte35MissingInTS = append(scte35MissingInTS, k)
		}
	}
	if len(scte35MissingInTS) > 0 {
		t.Errorf("Go SCTE35State has fields not declared in TypeScript SCTE35State: %v", scte35MissingInTS)
	}

	// =====================================================================
	// Nested type field name checks: SCTE35DescriptorInfo
	// =====================================================================
	scte35DescType := reflect.TypeOf(internal.SCTE35DescriptorInfo{})
	goDescFields := extractJSONTags(scte35DescType)
	tsDescFields := map[string]bool{
		"segEventId": true, "segmentationType": true, "segmentationTypeName": true,
		"upidType": true, "upidTypeName": true, "upid": true, "durationMs": true,
		"subSegmentNum": true, "subSegmentsExpected": true, "cancelled": true,
	}

	var descMissingInTS []string
	for k := range goDescFields {
		if !tsDescFields[k] {
			descMissingInTS = append(descMissingInTS, k)
		}
	}
	if len(descMissingInTS) > 0 {
		t.Errorf("Go SCTE35DescriptorInfo has fields not declared in TypeScript: %v", descMissingInTS)
	}

	// =====================================================================
	// Nested type: GraphicsLayerState
	// =====================================================================
	glsType := reflect.TypeOf(internal.GraphicsLayerState{})
	goGLSFields := extractJSONTags(glsType)
	tsGLSFields := map[string]bool{
		"id": true, "template": true, "active": true, "fadePosition": true,
		"animationMode": true, "animationHz": true, "zOrder": true,
		"x": true, "y": true, "width": true, "height": true,
		"imageName": true, "imageWidth": true, "imageHeight": true,
		"html5Url": true, "html5Format": true, "html5Status": true,
		"html5DataSchema": true,
		"sequenceLoaded":  true, "sequencePhase": true,
		"sequenceProcessing": true, "sequenceProgress": true,
		"sequenceSpeed": true,
	}

	var glsMissingInTS []string
	for k := range goGLSFields {
		if !tsGLSFields[k] {
			glsMissingInTS = append(glsMissingInTS, k)
		}
	}
	if len(glsMissingInTS) > 0 {
		t.Errorf("Go GraphicsLayerState has fields not in TypeScript: %v", glsMissingInTS)
	}

	// Check TS has fields Go doesn't
	var glsMissingInGo []string
	for k := range tsGLSFields {
		if !goGLSFields[k] {
			glsMissingInGo = append(glsMissingInGo, k)
		}
	}
	if len(glsMissingInGo) > 0 {
		t.Errorf("TypeScript GraphicsLayerState has fields not in Go: %v (browser will never receive these)", glsMissingInGo)
	}
}

// ==========================================================================
// TestStateBroadcastSequenceNumber
//
// Verifies that the Seq field in state broadcasts increases by exactly 1
// per operation (no gaps, no duplicates), and that each broadcast's state
// content matches the operation that produced it.
// ==========================================================================
func TestStateBroadcastSequenceNumber(t *testing.T) {
	sw := newSwitcherWithRawSources(t, "camA", "camB", "camC")

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Define the operations and expected state after each
	type expectedState struct {
		op      string
		program string
		preview string
	}
	ops := []expectedState{
		{"Cut(camA)", "camA", ""},
		{"SetPreview(camB)", "camA", "camB"},
		{"Cut(camC)", "camC", "camA"},
		{"Cut(camA)", "camA", "camC"},
	}

	// Perform operations
	err := sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}
	err = sw.SetPreview(context.Background(), "camB")
	if err != nil {
		t.Fatalf("SetPreview(camB) failed: %v", err)
	}
	err = sw.Cut(context.Background(), "camC")
	if err != nil {
		t.Fatalf("Cut(camC) failed: %v", err)
	}
	err = sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}

	cap.waitForStates(t, 4, 2*time.Second)

	// Assert exactly 4 broadcasts (one per operation)
	if got := cap.count(); got != 4 {
		t.Fatalf("expected exactly 4 broadcasts, got %d", got)
	}

	states := cap.all()
	seqs := make([]uint64, len(states))
	for i, s := range states {
		seqs[i] = s.Seq
	}
	t.Logf("MEASURE: seq values=%v", seqs)

	// Verify seq increases by exactly 1 between consecutive broadcasts (no gaps, no duplicates)
	for i := 1; i < len(seqs); i++ {
		gap := seqs[i] - seqs[i-1]
		t.Logf("MEASURE: seq gap[%d->%d] = %d", i-1, i, gap)
		if gap != 1 {
			t.Errorf("seq gap between broadcasts %d and %d is %d (expected exactly 1, no gaps)", i-1, i, gap)
		}
	}

	// Verify each broadcast's state content matches the operation
	for i, exp := range ops {
		s := states[i]
		t.Logf("MEASURE: broadcast[%d] (%s): program=%s preview=%s seq=%d",
			i, exp.op, s.ProgramSource, s.PreviewSource, s.Seq)
		if s.ProgramSource != exp.program {
			t.Errorf("broadcast[%d] (%s): ProgramSource=%q, expected %q",
				i, exp.op, s.ProgramSource, exp.program)
		}
		if s.PreviewSource != exp.preview {
			t.Errorf("broadcast[%d] (%s): PreviewSource=%q, expected %q",
				i, exp.op, s.PreviewSource, exp.preview)
		}
		// Every broadcast should have all 3 user sources.
		if countUserSources(s.Sources) != 3 {
			t.Errorf("broadcast[%d] (%s): user Sources count=%d, expected 3", i, exp.op, countUserSources(s.Sources))
		}
		// Tally should be consistent with program/preview
		if s.ProgramSource != "" && s.TallyState[s.ProgramSource] != "program" {
			t.Errorf("broadcast[%d] (%s): tally[%s]=%q, expected program",
				i, exp.op, s.ProgramSource, s.TallyState[s.ProgramSource])
		}
		if s.PreviewSource != "" && s.PreviewSource != s.ProgramSource && s.TallyState[s.PreviewSource] != "preview" {
			t.Errorf("broadcast[%d] (%s): tally[%s]=%q, expected preview",
				i, exp.op, s.PreviewSource, s.TallyState[s.PreviewSource])
		}
	}

	// Verify timestamps are non-decreasing
	for i := 1; i < len(states); i++ {
		if states[i].Timestamp < states[i-1].Timestamp {
			t.Errorf("timestamp decreased: states[%d].Timestamp=%d < states[%d].Timestamp=%d",
				i, states[i].Timestamp, i-1, states[i-1].Timestamp)
		}
	}
}

// ==========================================================================
// TestStateBroadcastContainsAudioLevels
//
// Verifies that when a mixer is attached to the switcher, state broadcasts
// contain non-zero audio fields (ProgramPeak, MasterLevel, LUFS, channels).
// This tests the full mixer→buildStateLocked→broadcast wiring.
// ==========================================================================
func TestStateBroadcastContainsAudioLevels(t *testing.T) {
	sw := newSwitcherWithRawSources(t, "camA", "camB")

	// Attach a mock mixer with known non-zero values
	mock := &mockAudioStateProvider{
		programPeak:    [2]float64{-6.0, -7.5},
		masterLevel:    -2.0,
		gainReduction:  3.0,
		limThreshold:   -1.0,
		limCeiling:     -0.5,
		limGR:          0.8,
		momentaryLUFS:  -22.0,
		shortTermLUFS:  -23.5,
		integratedLUFS: -24.0,
		channels: map[string]internal.AudioChannel{
			"camA": {Level: -3.0, Trim: 1.0, Muted: false, AFV: true, PeakL: -8.0, PeakR: -9.0},
			"camB": {Level: -12.0, Trim: 0.0, Muted: true, AFV: false, PeakL: -30.0, PeakR: -32.0},
		},
	}
	sw.SetMixer(mock)

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Cut to camA to trigger a broadcast
	err := sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}
	cap.waitForStates(t, 1, 2*time.Second)

	state := cap.last()
	t.Logf("MEASURE: ProgramPeak=%v, MasterLevel=%f, GainReduction=%f",
		state.ProgramPeak, state.MasterLevel, state.GainReduction)
	t.Logf("MEASURE: MomentaryLUFS=%f, ShortTermLUFS=%f, IntegratedLUFS=%f",
		state.MomentaryLUFS, state.ShortTermLUFS, state.IntegratedLUFS)

	// Verify ProgramPeak is exactly what the mock returns
	if state.ProgramPeak[0] != -6.0 {
		t.Errorf("ProgramPeak[0]: expected -6.0, got %f", state.ProgramPeak[0])
	}
	if state.ProgramPeak[1] != -7.5 {
		t.Errorf("ProgramPeak[1]: expected -7.5, got %f", state.ProgramPeak[1])
	}

	// Verify MasterLevel
	if state.MasterLevel != -2.0 {
		t.Errorf("MasterLevel: expected -2.0, got %f", state.MasterLevel)
	}

	// Verify GainReduction
	if state.GainReduction != 3.0 {
		t.Errorf("GainReduction: expected 3.0, got %f", state.GainReduction)
	}

	// Verify Limiter settings
	if state.Limiter == nil {
		t.Fatal("Limiter is nil — mixer→state wiring broken")
	}
	if state.Limiter.Threshold != -1.0 {
		t.Errorf("Limiter.Threshold: expected -1.0, got %f", state.Limiter.Threshold)
	}
	if state.Limiter.Ceiling != -0.5 {
		t.Errorf("Limiter.Ceiling: expected -0.5, got %f", state.Limiter.Ceiling)
	}
	if state.Limiter.GainReduction != 0.8 {
		t.Errorf("Limiter.GainReduction: expected 0.8, got %f", state.Limiter.GainReduction)
	}

	// Verify LUFS values
	if state.MomentaryLUFS != -22.0 {
		t.Errorf("MomentaryLUFS: expected -22.0, got %f", state.MomentaryLUFS)
	}
	if state.ShortTermLUFS != -23.5 {
		t.Errorf("ShortTermLUFS: expected -23.5, got %f", state.ShortTermLUFS)
	}
	if state.IntegratedLUFS != -24.0 {
		t.Errorf("IntegratedLUFS: expected -24.0, got %f", state.IntegratedLUFS)
	}

	// Verify AudioChannels are populated from the mock
	if state.AudioChannels == nil {
		t.Fatal("AudioChannels is nil — mixer→state wiring broken")
	}
	if len(state.AudioChannels) != 2 {
		t.Errorf("expected 2 audio channels, got %d", len(state.AudioChannels))
	}

	// Verify camA channel details
	chA, ok := state.AudioChannels["camA"]
	if !ok {
		t.Fatal("AudioChannels missing camA")
	}
	if chA.Level != -3.0 {
		t.Errorf("AudioChannels[camA].Level: expected -3.0, got %f", chA.Level)
	}
	if chA.Trim != 1.0 {
		t.Errorf("AudioChannels[camA].Trim: expected 1.0, got %f", chA.Trim)
	}
	if chA.Muted {
		t.Error("AudioChannels[camA].Muted: expected false, got true")
	}
	if !chA.AFV {
		t.Error("AudioChannels[camA].AFV: expected true, got false")
	}
	if chA.PeakL != -8.0 {
		t.Errorf("AudioChannels[camA].PeakL: expected -8.0, got %f", chA.PeakL)
	}
	if chA.PeakR != -9.0 {
		t.Errorf("AudioChannels[camA].PeakR: expected -9.0, got %f", chA.PeakR)
	}

	// Verify camB channel (muted source)
	chB, ok := state.AudioChannels["camB"]
	if !ok {
		t.Fatal("AudioChannels missing camB")
	}
	if !chB.Muted {
		t.Error("AudioChannels[camB].Muted: expected true, got false")
	}
	if chB.AFV {
		t.Error("AudioChannels[camB].AFV: expected false, got true")
	}

	// =====================================================================
	// Verify that without a mixer, audio fields are zero-valued
	// =====================================================================
	sw2 := newSwitcherWithRawSources(t, "src1")
	// Intentionally do NOT call sw2.SetMixer()
	cap2 := &stateCapture{}
	sw2.OnStateChange(cap2.callback)

	err = sw2.Cut(context.Background(), "src1")
	if err != nil {
		t.Fatalf("Cut(src1) failed: %v", err)
	}
	cap2.waitForStates(t, 1, 2*time.Second)

	noMixerState := cap2.last()
	t.Logf("MEASURE: no-mixer state: ProgramPeak=%v, AudioChannels=%v, Limiter=%v",
		noMixerState.ProgramPeak, noMixerState.AudioChannels, noMixerState.Limiter)

	if noMixerState.ProgramPeak != [2]float64{0, 0} {
		t.Errorf("no-mixer ProgramPeak should be zero, got %v", noMixerState.ProgramPeak)
	}
	if noMixerState.AudioChannels != nil {
		t.Errorf("no-mixer AudioChannels should be nil, got %v", noMixerState.AudioChannels)
	}
	if noMixerState.Limiter != nil {
		t.Errorf("no-mixer Limiter should be nil, got %v", noMixerState.Limiter)
	}
}

// ==========================================================================
// TestStatePublisherCallbackInvoked
//
// Verifies that the control.StatePublisher correctly serializes and
// invokes its callback.
// ==========================================================================
func TestStatePublisherCallbackInvoked(t *testing.T) {
	var mu sync.Mutex
	var captured [][]byte

	pub := control.NewStatePublisher(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]byte, len(data))
		copy(cp, data)
		captured = append(captured, cp)
	})

	state := internal.ControlRoomState{
		ProgramSource: "test",
		Seq:           1,
		Timestamp:     time.Now().UnixMilli(),
		TallyState:    map[string]string{"test": "program"},
		Sources:       map[string]internal.SourceInfo{},
	}

	pub.Publish(state)

	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(captured))
	}

	// Verify the callback data is valid JSON
	var decoded internal.ControlRoomState
	if err := json.Unmarshal(captured[0], &decoded); err != nil {
		t.Fatalf("callback data is not valid JSON: %v", err)
	}

	t.Logf("MEASURE: publisher JSON size=%d bytes, programSource=%s, seq=%d",
		len(captured[0]), decoded.ProgramSource, decoded.Seq)

	if decoded.ProgramSource != "test" {
		t.Errorf("expected ProgramSource=test, got %q", decoded.ProgramSource)
	}
	if decoded.Seq != 1 {
		t.Errorf("expected Seq=1, got %d", decoded.Seq)
	}
}

// ==========================================================================
// TestChannelPublisherDropOldest
//
// Verifies the ChannelPublisher drops oldest messages when the buffer is full.
// ==========================================================================
func TestChannelPublisherDropOldest(t *testing.T) {
	pub := control.NewChannelPublisher(2) // buffer size 2

	// Publish 3 messages (buffer can hold 2)
	for i := 0; i < 3; i++ {
		state := internal.ControlRoomState{
			ProgramSource: "test",
			Seq:           uint64(i + 1),
			Timestamp:     time.Now().UnixMilli(),
			TallyState:    map[string]string{},
			Sources:       map[string]internal.SourceInfo{},
		}
		pub.Publish(state)
	}

	dropped := pub.DroppedCount()
	t.Logf("MEASURE: dropped count after 3 publishes to buf-size-2: %d", dropped)

	if dropped < 1 {
		t.Errorf("expected at least 1 dropped message, got %d", dropped)
	}

	// Read the remaining messages from the channel
	ch := pub.Ch()
	var received []uint64
	for {
		select {
		case data := <-ch:
			var s internal.ControlRoomState
			if err := json.Unmarshal(data, &s); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			received = append(received, s.Seq)
		default:
			goto done
		}
	}
done:

	t.Logf("MEASURE: received seqs from channel: %v", received)

	// Should have 2 messages remaining in channel
	if len(received) != 2 {
		t.Errorf("expected 2 messages in channel, got %d", len(received))
	}

	// The newest messages should be present (oldest dropped)
	// With buf size 2 and 3 publishes, seq 1 should be dropped, 2 and 3 remain
	if len(received) == 2 {
		if received[0] != 2 || received[1] != 3 {
			t.Errorf("expected seqs [2 3], got %v (oldest should be dropped)", received)
		}
	}
}

// ==========================================================================
// Helpers
// ==========================================================================

func sourceKeys(sources map[string]internal.SourceInfo) []string {
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func extractJSONTags(t reflect.Type) map[string]bool {
	tags := make(map[string]bool)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		tags[name] = true
	}
	return tags
}

// mockAudioStateProvider implements the audioStateProvider interface for testing
// the mixer→state→broadcast wiring without needing a real audio mixer.
type mockAudioStateProvider struct {
	programPeak    [2]float64
	channels       map[string]internal.AudioChannel
	masterLevel    float64
	gainReduction  float64
	limThreshold   float64
	limCeiling     float64
	limGR          float64
	momentaryLUFS  float64
	shortTermLUFS  float64
	integratedLUFS float64
}

func (m *mockAudioStateProvider) ProgramPeak() [2]float64                         { return m.programPeak }
func (m *mockAudioStateProvider) ChannelStates() map[string]internal.AudioChannel { return m.channels }
func (m *mockAudioStateProvider) MasterLevel() float64                            { return m.masterLevel }
func (m *mockAudioStateProvider) GainReduction() float64                          { return m.gainReduction }
func (m *mockAudioStateProvider) LimiterSettings() (float64, float64, float64, float64) {
	return m.limThreshold, m.limCeiling, m.limGR, -144.0
}
func (m *mockAudioStateProvider) MomentaryLUFS() float64  { return m.momentaryLUFS }
func (m *mockAudioStateProvider) ShortTermLUFS() float64  { return m.shortTermLUFS }
func (m *mockAudioStateProvider) IntegratedLUFS() float64 { return m.integratedLUFS }
