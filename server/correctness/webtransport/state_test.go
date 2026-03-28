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

// ==========================================================================
// TestStateBroadcastOnCut
//
// Verifies that Cut operations produce correct state broadcasts with
// accurate ProgramSource and TallyState fields.
// ==========================================================================
func TestStateBroadcastOnCut(t *testing.T) {
	sw := newSwitcherWithRawSources(t, "camA", "camB")

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Cut to camA
	err := sw.Cut(context.Background(), "camA")
	if err != nil {
		t.Fatalf("Cut(camA) failed: %v", err)
	}

	cap.waitForStates(t, 1, 2*time.Second)
	state1 := cap.last()
	t.Logf("MEASURE: after Cut(camA): program=%s, preview=%s, tally=%v",
		state1.ProgramSource, state1.PreviewSource, state1.TallyState)

	// Verify programSource
	if state1.ProgramSource != "camA" {
		t.Errorf("expected ProgramSource=camA, got %q", state1.ProgramSource)
	}

	// Verify tally: camA should be program
	if state1.TallyState["camA"] != "program" {
		t.Errorf("expected tally[camA]=program, got %q", state1.TallyState["camA"])
	}

	// camB should be idle (not on program or preview)
	if state1.TallyState["camB"] != "idle" {
		t.Errorf("expected tally[camB]=idle, got %q", state1.TallyState["camB"])
	}

	// Cut to camB
	countBefore := cap.count()
	err = sw.Cut(context.Background(), "camB")
	if err != nil {
		t.Fatalf("Cut(camB) failed: %v", err)
	}

	cap.waitForStates(t, countBefore+1, 2*time.Second)
	state2 := cap.last()
	t.Logf("MEASURE: after Cut(camB): program=%s, preview=%s, tally=%v",
		state2.ProgramSource, state2.PreviewSource, state2.TallyState)

	// Verify programSource changed
	if state2.ProgramSource != "camB" {
		t.Errorf("expected ProgramSource=camB, got %q", state2.ProgramSource)
	}

	// Verify preview: camA should move to preview
	if state2.PreviewSource != "camA" {
		t.Errorf("expected PreviewSource=camA, got %q", state2.PreviewSource)
	}

	// Verify tally: camB=program, camA=preview
	if state2.TallyState["camB"] != "program" {
		t.Errorf("expected tally[camB]=program, got %q", state2.TallyState["camB"])
	}
	if state2.TallyState["camA"] != "preview" {
		t.Errorf("expected tally[camA]=preview, got %q", state2.TallyState["camA"])
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

	// Both sources should be present
	if len(state2.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(state2.Sources))
	}
}

// ==========================================================================
// TestStateBroadcastSerialization
//
// Verifies JSON round-trip of ControlRoomState and checks that Go JSON
// field names match what the TypeScript client expects.
// ==========================================================================
func TestStateBroadcastSerialization(t *testing.T) {
	// Build a ControlRoomState with many fields populated
	markIn := int64(1000)
	markOut := int64(5000)
	state := internal.ControlRoomState{
		ProgramSource:        "camA",
		PreviewSource:        "camB",
		TransitionType:       "mix",
		TransitionDurationMs: 500,
		TransitionPosition:   0.5,
		TransitionEasing:     "smoothstep",
		InTransition:         true,
		FTBActive:            false,
		AudioChannels: map[string]internal.AudioChannel{
			"camA": {
				Level: -6.0,
				Trim:  0.0,
				Muted: false,
				AFV:   true,
				PeakL: -12.0,
				PeakR: -14.0,
				EQ: [3]internal.EQBand{
					{Frequency: 100, Gain: 0, Q: 1.0, Enabled: true},
					{Frequency: 1000, Gain: 2.0, Q: 0.7, Enabled: true},
					{Frequency: 8000, Gain: -1.0, Q: 1.4, Enabled: false},
				},
				Compressor: internal.CompressorSettings{
					Threshold: -20.0, Ratio: 4.0, Attack: 10.0, Release: 100.0, MakeupGain: 3.0,
				},
				GainReduction: 2.5,
				Gate: internal.GateSettings{
					Threshold: -40.0, Range: -80.0, Attack: 0.5, Release: 50.0, Hold: 10.0,
				},
			},
		},
		MasterLevel:    0.0,
		ProgramPeak:    [2]float64{-8.0, -9.0},
		GainReduction:  1.5,
		MomentaryLUFS:  -23.0,
		ShortTermLUFS:  -24.0,
		IntegratedLUFS: -24.5,
		Limiter: &internal.LimiterSettings{
			Threshold: -1.0, Ceiling: -0.5, GainReduction: 0.3,
		},
		TallyState: map[string]string{
			"camA": "program",
			"camB": "preview",
		},
		Sources: map[string]internal.SourceInfo{
			"camA": {Key: "camA", Label: "Camera A", Status: "healthy", Position: 1},
			"camB": {Key: "camB", Label: "Camera B", Status: "healthy", Position: 2},
		},
		Presets: []internal.PresetInfo{
			{ID: "p1", Name: "Default"},
		},
		Recording: &internal.RecordingStatus{
			Active: true, Filename: "rec001.ts", BytesWritten: 1048576,
		},
		SRTOutput: &internal.SRTOutputStatus{
			Active: true, Mode: "caller", Address: "192.168.1.100", Port: 9000,
		},
		Destinations: []internal.DestinationInfo{
			{ID: "d1", Type: "srt-caller", State: "connected"},
		},
		Graphics: &internal.GraphicsState{
			Layers: []internal.GraphicsLayerState{
				{ID: 1, Active: true, ZOrder: 0, X: 0, Y: 0, Width: 1920, Height: 1080},
			},
			ProgramWidth: 1920, ProgramHeight: 1080,
		},
		Layout: &internal.LayoutState{
			ActivePreset: "pip",
			Slots: []internal.LayoutSlotState{
				{ID: 0, SourceKey: "camB", Enabled: true, X: 100, Y: 100, Width: 480, Height: 270, ZOrder: 1},
			},
		},
		Replay: &internal.ReplayState{
			State:  "idle",
			Source: "camA",
			Speed:  1.0,
			MarkIn: &markIn, MarkOut: &markOut,
			Buffers: []internal.ReplayBufferInfo{
				{Source: "camA", FrameCount: 900, GOPCount: 30, DurationSecs: 30.0, BytesUsed: 10485760},
			},
			ShutterAngle:   180.0,
			GPUAccelerated: true,
		},
		ClipPlayers: []internal.ClipPlayerInfo{
			{ID: 0, State: "empty"},
			{ID: 1, State: "loaded", ClipID: "clip1", ClipName: "Intro"},
		},
		ClipCount: 5,
		Operators: []internal.OperatorInfo{
			{ID: "op1", Name: "Director", Role: "director", Connected: true},
		},
		Locks: map[string]internal.LockInfo{
			"switcher": {HolderID: "op1", HolderName: "Director", AcquiredAt: 1700000000000},
		},
		PipelineFormat: &internal.PipelineFormatInfo{
			Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Name: "1080p30",
		},
		Encoder: &internal.EncoderState{
			Current: "x264",
			Available: []internal.EncoderInfo{
				{Name: "x264", DisplayName: "x264 (Software)", IsDefault: true},
			},
		},
		SCTE35: &internal.SCTE35State{
			Enabled:        true,
			SCTE104Enabled: true,
			ActiveEvents:   map[uint32]internal.SCTE35Active{},
			EventLog:       []internal.SCTE35Event{},
			HeartbeatOK:    true,
			Config: internal.SCTE35Config{
				HeartbeatIntervalMs: 5000,
				DefaultPreRollMs:    4000,
				PID:                 500,
			},
		},
		CBR: &internal.CBRStatus{
			Enabled:    true,
			MuxrateBps: 20000000,
		},
		ConnectionInfo: &internal.ConnectionInfo{
			Domain:         "switchframe.dev",
			SRTIngestPort:  9000,
			SRTOutputPorts: []int{9001, 9002},
		},
		Captions: &internal.CaptionState{
			Mode: "off",
		},
		Comms: &internal.CommsState{
			Active: true,
			Participants: []internal.CommsParticipant{
				{OperatorID: "op1", Name: "Director", Muted: false, Speaking: true},
			},
		},
		STMap: &internal.STMapState{
			Sources:   map[string]string{"camA": "barrel"},
			Available: []string{"barrel", "pincushion"},
		},
		Macro: &internal.MacroExecutionState{
			Running:   true,
			MacroName: "OpeningSequence",
			Steps: []internal.MacroStepState{
				{Action: "cut", Summary: "Cut to camA", Status: "done"},
				{Action: "wait", Summary: "Wait 2s", Status: "running", WaitMs: 2000},
			},
			CurrentStep: 1,
		},
		Playout: &internal.PlayoutState{
			Channels: []internal.PlayoutChannelInfo{
				{ID: 0, State: "on_air", PlaylistName: "Morning Show"},
			},
		},
		LastChangedBy: "op1",
		Seq:           42,
		Timestamp:     time.Now().UnixMilli(),
	}

	// Marshal to JSON
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	t.Logf("MEASURE: JSON size=%d bytes", len(data))

	// Unmarshal back
	var decoded internal.ControlRoomState
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// Compare key fields
	t.Logf("MEASURE: fields preserved: program=%v, preview=%v, tally=%v, sources=%v, seq=%v",
		decoded.ProgramSource == state.ProgramSource,
		decoded.PreviewSource == state.PreviewSource,
		reflect.DeepEqual(decoded.TallyState, state.TallyState),
		len(decoded.Sources) == len(state.Sources),
		decoded.Seq == state.Seq)

	// Verify exact field match for critical fields
	if decoded.ProgramSource != state.ProgramSource {
		t.Errorf("ProgramSource mismatch: %q vs %q", decoded.ProgramSource, state.ProgramSource)
	}
	if decoded.PreviewSource != state.PreviewSource {
		t.Errorf("PreviewSource mismatch: %q vs %q", decoded.PreviewSource, state.PreviewSource)
	}
	if !reflect.DeepEqual(decoded.TallyState, state.TallyState) {
		t.Errorf("TallyState mismatch: %v vs %v", decoded.TallyState, state.TallyState)
	}
	if decoded.TransitionType != state.TransitionType {
		t.Errorf("TransitionType mismatch: %q vs %q", decoded.TransitionType, state.TransitionType)
	}
	if decoded.TransitionDurationMs != state.TransitionDurationMs {
		t.Errorf("TransitionDurationMs mismatch: %d vs %d", decoded.TransitionDurationMs, state.TransitionDurationMs)
	}
	if decoded.TransitionPosition != state.TransitionPosition {
		t.Errorf("TransitionPosition mismatch: %f vs %f", decoded.TransitionPosition, state.TransitionPosition)
	}
	if decoded.InTransition != state.InTransition {
		t.Errorf("InTransition mismatch: %v vs %v", decoded.InTransition, state.InTransition)
	}
	if decoded.FTBActive != state.FTBActive {
		t.Errorf("FTBActive mismatch: %v vs %v", decoded.FTBActive, state.FTBActive)
	}
	if decoded.MasterLevel != state.MasterLevel {
		t.Errorf("MasterLevel mismatch: %f vs %f", decoded.MasterLevel, state.MasterLevel)
	}
	if decoded.ProgramPeak != state.ProgramPeak {
		t.Errorf("ProgramPeak mismatch: %v vs %v", decoded.ProgramPeak, state.ProgramPeak)
	}
	if decoded.Seq != state.Seq {
		t.Errorf("Seq mismatch: %d vs %d", decoded.Seq, state.Seq)
	}
	if decoded.Timestamp != state.Timestamp {
		t.Errorf("Timestamp mismatch: %d vs %d", decoded.Timestamp, state.Timestamp)
	}

	// Verify audio channels round-trip correctly
	if decoded.AudioChannels == nil {
		t.Fatal("AudioChannels is nil after round-trip")
	}
	origCh := state.AudioChannels["camA"]
	decodedCh, ok := decoded.AudioChannels["camA"]
	if !ok {
		t.Fatal("AudioChannel camA missing after round-trip")
	}
	if decodedCh.Level != origCh.Level {
		t.Errorf("AudioChannel.Level mismatch: %f vs %f", decodedCh.Level, origCh.Level)
	}
	if decodedCh.AFV != origCh.AFV {
		t.Errorf("AudioChannel.AFV mismatch: %v vs %v", decodedCh.AFV, origCh.AFV)
	}
	if decodedCh.EQ != origCh.EQ {
		t.Errorf("AudioChannel.EQ mismatch: %v vs %v", decodedCh.EQ, origCh.EQ)
	}

	// Verify optional struct fields survived
	if decoded.Recording == nil || !decoded.Recording.Active {
		t.Errorf("Recording field lost in round-trip")
	}
	if decoded.SRTOutput == nil || decoded.SRTOutput.Port != 9000 {
		t.Errorf("SRTOutput field lost in round-trip")
	}
	if decoded.Graphics == nil || len(decoded.Graphics.Layers) != 1 {
		t.Errorf("Graphics field lost in round-trip")
	}
	if decoded.Layout == nil || decoded.Layout.ActivePreset != "pip" {
		t.Errorf("Layout field lost in round-trip")
	}
	if decoded.Replay == nil || decoded.Replay.State != "idle" {
		t.Errorf("Replay field lost in round-trip")
	}
	if decoded.SCTE35 == nil || !decoded.SCTE35.Enabled {
		t.Errorf("SCTE35 field lost in round-trip")
	}
	if decoded.CBR == nil || !decoded.CBR.Enabled {
		t.Errorf("CBR field lost in round-trip")
	}
	if decoded.Captions == nil || decoded.Captions.Mode != "off" {
		t.Errorf("Captions field lost in round-trip")
	}
	if decoded.Comms == nil || !decoded.Comms.Active {
		t.Errorf("Comms field lost in round-trip")
	}
	if decoded.STMap == nil || len(decoded.STMap.Available) != 2 {
		t.Errorf("STMap field lost in round-trip")
	}
	if decoded.Macro == nil || !decoded.Macro.Running {
		t.Errorf("Macro field lost in round-trip")
	}
	if decoded.Playout == nil || len(decoded.Playout.Channels) != 1 {
		t.Errorf("Playout field lost in round-trip")
	}
	if decoded.ConnectionInfo == nil || decoded.ConnectionInfo.Domain != "switchframe.dev" {
		t.Errorf("ConnectionInfo field lost in round-trip")
	}
	if decoded.Encoder == nil || decoded.Encoder.Current != "x264" {
		t.Errorf("Encoder field lost in round-trip")
	}

	// =====================================================================
	// JSON field name verification against TypeScript expectations
	// =====================================================================
	// Parse the JSON into a raw map to inspect actual field names
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		t.Fatalf("unmarshal to raw map failed: %v", err)
	}

	// These are the field names the TypeScript ControlRoomState interface expects.
	// Extracted from ui/src/lib/api/types.ts ControlRoomState interface.
	expectedTSFields := map[string]bool{
		"programSource":        true,
		"previewSource":        true,
		"transitionType":       true,
		"transitionDurationMs": true,
		"transitionPosition":   true,
		"transitionEasing":     true,
		"inTransition":         true,
		"ftbActive":            true,
		"audioChannels":        true,
		"masterLevel":          true,
		"programPeak":          true,
		"gainReduction":        true,
		"limiter":              true,
		"momentaryLufs":        true,
		"shortTermLufs":        true,
		"integratedLufs":       true,
		"tallyState":           true,
		"sources":              true,
		"presets":              true,
		"recording":            true,
		"srtOutput":            true,
		"destinations":         true,
		"graphics":             true,
		"layout":               true,
		"replay":               true,
		"operators":            true,
		"locks":                true,
		"pipelineFormat":       true,
		"encoder":              true,
		"scte35":               true,
		"connectionInfo":       true,
		"captions":             true,
		"asr":                  true,
		"clipPlayers":          true,
		"clipCount":            true,
		"clipUpload":           true,
		"macro":                true,
		"comms":                true,
		"stmap":                true,
		"colorGrade":           true,
		"aiSegmentation":       true,
		"neuralKey":            true,
		"arOcclusion":          true,
		"arTracking":           true,
		"arScene":              true,
		"playout":              true,
		"lastChangedBy":        true,
		"seq":                  true,
		"timestamp":            true,
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
		"position": true, "delayMs": true, "isVirtual": true, "hasCaptions": true,
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
		"buffers": true,
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
		"enabled": true, "activeEvents": true, "eventLog": true,
		"heartbeatOk": true, "config": true,
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
		"segEventId": true, "segmentationType": true, "upidType": true,
		"upid": true, "durationTicks": true, "subSegmentNum": true,
		"subSegmentsExpected": true,
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
		// TS also has these extra fields not in Go struct:
		"sequenceLoaded": true, "sequencePhase": true,
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
// Verifies that the Seq field in state broadcasts increases monotonically
// across multiple operations.
// ==========================================================================
func TestStateBroadcastSequenceNumber(t *testing.T) {
	sw := newSwitcherWithRawSources(t, "camA", "camB", "camC")

	cap := &stateCapture{}
	sw.OnStateChange(cap.callback)

	// Perform several operations that trigger state broadcasts
	// Each should increment the sequence number
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

	states := cap.all()
	seqs := make([]uint64, len(states))
	for i, s := range states {
		seqs[i] = s.Seq
	}
	t.Logf("MEASURE: seq values=%v", seqs)

	// Verify monotonically increasing
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq not monotonically increasing: seq[%d]=%d <= seq[%d]=%d",
				i, seqs[i], i-1, seqs[i-1])
		}
	}

	// Verify no gaps of more than 1 between consecutive broadcasts
	// (RegisterMXLSource also increments seq, so initial seq may be > 0)
	// But within our own operations, each should increment by exactly 1
	for i := 1; i < len(seqs); i++ {
		gap := seqs[i] - seqs[i-1]
		t.Logf("MEASURE: seq gap[%d→%d] = %d", i-1, i, gap)
		if gap != 1 {
			t.Errorf("seq gap between broadcasts %d and %d is %d (expected 1)", i-1, i, gap)
		}
	}

	// Verify timestamps are also non-decreasing
	for i := 1; i < len(states); i++ {
		if states[i].Timestamp < states[i-1].Timestamp {
			t.Errorf("timestamp decreased: states[%d].Timestamp=%d < states[%d].Timestamp=%d",
				i, states[i].Timestamp, i-1, states[i-1].Timestamp)
		}
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
