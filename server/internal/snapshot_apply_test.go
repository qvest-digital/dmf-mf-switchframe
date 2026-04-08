package internal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// --- Mock implementation of SnapshotApplyTarget ---

// mockCall records a single method invocation on the mock target.
type mockCall struct {
	Method string
	Args   []any
}

// mockApplyTarget records all calls and optionally returns errors for specific methods.
type mockApplyTarget struct {
	calls  []mockCall
	errors map[string]error // method name → error to return
}

func newMockApplyTarget() *mockApplyTarget {
	return &mockApplyTarget{
		errors: make(map[string]error),
	}
}

func (m *mockApplyTarget) record(method string, args ...any) error {
	m.calls = append(m.calls, mockCall{Method: method, Args: args})
	if err, ok := m.errors[method]; ok {
		return err
	}
	return nil
}

// callCount returns how many times a method was called.
func (m *mockApplyTarget) callCount(method string) int {
	n := 0
	for _, c := range m.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// callsFor returns all calls to a given method.
func (m *mockApplyTarget) callsFor(method string) []mockCall {
	var out []mockCall
	for _, c := range m.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// wasCalled returns true if the method was called at least once.
func (m *mockApplyTarget) wasCalled(method string) bool {
	return m.callCount(method) > 0
}

// --- SnapshotApplyTarget interface methods ---

func (m *mockApplyTarget) Cut(ctx context.Context, source string) error {
	return m.record("Cut", source)
}

func (m *mockApplyTarget) SetPreview(ctx context.Context, source string) error {
	return m.record("SetPreview", source)
}

func (m *mockApplyTarget) SetTransitionConfig(transitionType string, durationMs int, easing string) error {
	return m.record("SetTransitionConfig", transitionType, durationMs, easing)
}

func (m *mockApplyTarget) SetFTB(active bool) error {
	return m.record("SetFTB", active)
}

func (m *mockApplyTarget) SetLevel(sourceKey string, levelDB float64) error {
	return m.record("SetLevel", sourceKey, levelDB)
}

func (m *mockApplyTarget) SetTrim(sourceKey string, trimDB float64) error {
	return m.record("SetTrim", sourceKey, trimDB)
}

func (m *mockApplyTarget) SetMuted(sourceKey string, muted bool) error {
	return m.record("SetMuted", sourceKey, muted)
}

func (m *mockApplyTarget) SetAFV(sourceKey string, afv bool) error {
	return m.record("SetAFV", sourceKey, afv)
}

func (m *mockApplyTarget) SetPhaseInvert(sourceKey string, invert bool) error {
	return m.record("SetPhaseInvert", sourceKey, invert)
}

func (m *mockApplyTarget) SetBalance(sourceKey string, balance float64) error {
	return m.record("SetBalance", sourceKey, balance)
}

func (m *mockApplyTarget) SetHPF(sourceKey string, frequency float64, enabled bool) error {
	return m.record("SetHPF", sourceKey, frequency, enabled)
}

func (m *mockApplyTarget) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	return m.record("SetEQ", sourceKey, band, frequency, gain, q, enabled)
}

func (m *mockApplyTarget) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	return m.record("SetCompressor", sourceKey, threshold, ratio, attack, release, makeupGain)
}

func (m *mockApplyTarget) SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error {
	return m.record("SetGate", sourceKey, threshold, rangeDB, attack, release, hold, enabled)
}

func (m *mockApplyTarget) SetAudioDelay(sourceKey string, delayMs int) error {
	return m.record("SetAudioDelay", sourceKey, delayMs)
}

func (m *mockApplyTarget) SetMasterLevel(levelDB float64) error {
	return m.record("SetMasterLevel", levelDB)
}

func (m *mockApplyTarget) SetLimiterThreshold(thresholdDB float64) error {
	return m.record("SetLimiterThreshold", thresholdDB)
}

func (m *mockApplyTarget) SetLimiterCeiling(ceilingDB float64) error {
	return m.record("SetLimiterCeiling", ceilingDB)
}

func (m *mockApplyTarget) SetGraphicsLayerActive(id int, active bool) error {
	return m.record("SetGraphicsLayerActive", id, active)
}

func (m *mockApplyTarget) SetGraphicsLayerZOrder(id int, zOrder int) error {
	return m.record("SetGraphicsLayerZOrder", id, zOrder)
}

func (m *mockApplyTarget) SetGraphicsLayerRect(id int, x, y, width, height int) error {
	return m.record("SetGraphicsLayerRect", id, x, y, width, height)
}

func (m *mockApplyTarget) SetDVELayout(layout LayoutSnapshot) error {
	return m.record("SetDVELayout", layout)
}

func (m *mockApplyTarget) SetSourceLabel(ctx context.Context, sourceKey, label string) error {
	return m.record("SetSourceLabel", sourceKey, label)
}

func (m *mockApplyTarget) SetSourcePosition(sourceKey string, position int) error {
	return m.record("SetSourcePosition", sourceKey, position)
}

func (m *mockApplyTarget) SetUpstreamKey(sourceKey string, config UpstreamKeySnapshot) error {
	return m.record("SetUpstreamKey", sourceKey, config)
}

func (m *mockApplyTarget) SetCaptionMode(mode string) error {
	return m.record("SetCaptionMode", mode)
}

func (m *mockApplyTarget) SetColorGrade(lookName string, intensity float32) error {
	return m.record("SetColorGrade", lookName, intensity)
}

func (m *mockApplyTarget) DisableColorGrade() error {
	return m.record("DisableColorGrade")
}

func (m *mockApplyTarget) AssignSTMapSource(sourceKey, mapName string) error {
	return m.record("AssignSTMapSource", sourceKey, mapName)
}

func (m *mockApplyTarget) AssignSTMapProgram(mapName string) error {
	return m.record("AssignSTMapProgram", mapName)
}

// --- Tests ---

func TestApplyFullSnapshot_AllFieldsApplied(t *testing.T) {
	mock := newMockApplyTarget()
	snap := populatedSnapshot()
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	// populatedSnapshot() has no ClipPlayerPositions, so no clip warnings expected.
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	// 1. Transition config
	if !mock.wasCalled("SetTransitionConfig") {
		t.Error("SetTransitionConfig not called")
	} else {
		c := mock.callsFor("SetTransitionConfig")[0]
		if c.Args[0] != "mix" || c.Args[1] != 1000 || c.Args[2] != "smoothstep" {
			t.Errorf("SetTransitionConfig args: got %v", c.Args)
		}
	}

	// 2. Audio channels — both cam1 and cam2 should be set
	if mock.callCount("SetLevel") != 2 {
		t.Errorf("SetLevel: expected 2 calls, got %d", mock.callCount("SetLevel"))
	}
	if mock.callCount("SetTrim") != 2 {
		t.Errorf("SetTrim: expected 2 calls, got %d", mock.callCount("SetTrim"))
	}
	if mock.callCount("SetMuted") != 2 {
		t.Errorf("SetMuted: expected 2 calls, got %d", mock.callCount("SetMuted"))
	}
	if mock.callCount("SetAFV") != 2 {
		t.Errorf("SetAFV: expected 2 calls, got %d", mock.callCount("SetAFV"))
	}
	if mock.callCount("SetPhaseInvert") != 2 {
		t.Errorf("SetPhaseInvert: expected 2 calls, got %d", mock.callCount("SetPhaseInvert"))
	}
	if mock.callCount("SetBalance") != 2 {
		t.Errorf("SetBalance: expected 2 calls, got %d", mock.callCount("SetBalance"))
	}
	if mock.callCount("SetHPF") != 2 {
		t.Errorf("SetHPF: expected 2 calls, got %d", mock.callCount("SetHPF"))
	}
	// 3 EQ bands per channel, 2 channels = 6
	if mock.callCount("SetEQ") != 6 {
		t.Errorf("SetEQ: expected 6 calls, got %d", mock.callCount("SetEQ"))
	}
	if mock.callCount("SetCompressor") != 2 {
		t.Errorf("SetCompressor: expected 2 calls, got %d", mock.callCount("SetCompressor"))
	}
	if mock.callCount("SetGate") != 2 {
		t.Errorf("SetGate: expected 2 calls, got %d", mock.callCount("SetGate"))
	}
	if mock.callCount("SetAudioDelay") != 2 {
		t.Errorf("SetAudioDelay: expected 2 calls, got %d", mock.callCount("SetAudioDelay"))
	}

	// 3. Audio master
	if !mock.wasCalled("SetMasterLevel") {
		t.Error("SetMasterLevel not called")
	} else {
		c := mock.callsFor("SetMasterLevel")[0]
		if c.Args[0] != -8.0 {
			t.Errorf("SetMasterLevel arg: got %v, want -8.0", c.Args[0])
		}
	}
	if !mock.wasCalled("SetLimiterThreshold") {
		t.Error("SetLimiterThreshold not called")
	}
	if !mock.wasCalled("SetLimiterCeiling") {
		t.Error("SetLimiterCeiling not called")
	}

	// 4. Program source (Cut)
	if !mock.wasCalled("Cut") {
		t.Error("Cut not called")
	} else {
		c := mock.callsFor("Cut")[0]
		if c.Args[0] != "cam1" {
			t.Errorf("Cut arg: got %v, want cam1", c.Args[0])
		}
	}

	// 5. Preview source
	if !mock.wasCalled("SetPreview") {
		t.Error("SetPreview not called")
	} else {
		c := mock.callsFor("SetPreview")[0]
		if c.Args[0] != "cam2" {
			t.Errorf("SetPreview arg: got %v, want cam2", c.Args[0])
		}
	}

	// 6. FTB
	if !mock.wasCalled("SetFTB") {
		t.Error("SetFTB not called")
	}

	// 7. Graphics layers (2 layers)
	if mock.callCount("SetGraphicsLayerActive") != 2 {
		t.Errorf("SetGraphicsLayerActive: expected 2 calls, got %d", mock.callCount("SetGraphicsLayerActive"))
	}
	if mock.callCount("SetGraphicsLayerZOrder") != 2 {
		t.Errorf("SetGraphicsLayerZOrder: expected 2 calls, got %d", mock.callCount("SetGraphicsLayerZOrder"))
	}
	if mock.callCount("SetGraphicsLayerRect") != 2 {
		t.Errorf("SetGraphicsLayerRect: expected 2 calls, got %d", mock.callCount("SetGraphicsLayerRect"))
	}

	// 8. DVE layout
	if !mock.wasCalled("SetDVELayout") {
		t.Error("SetDVELayout not called")
	}

	// 9. Source config (3 sources: cam1, cam2, cam3)
	if mock.callCount("SetSourceLabel") != 3 {
		t.Errorf("SetSourceLabel: expected 3 calls, got %d", mock.callCount("SetSourceLabel"))
	}
	if mock.callCount("SetSourcePosition") != 3 {
		t.Errorf("SetSourcePosition: expected 3 calls, got %d", mock.callCount("SetSourcePosition"))
	}

	// 10. Upstream keys (cam1)
	if mock.callCount("SetUpstreamKey") != 1 {
		t.Errorf("SetUpstreamKey: expected 1 call, got %d", mock.callCount("SetUpstreamKey"))
	} else {
		c := mock.callsFor("SetUpstreamKey")[0]
		if c.Args[0] != "cam1" {
			t.Errorf("SetUpstreamKey source: got %v, want cam1", c.Args[0])
		}
		cfg := c.Args[1].(UpstreamKeySnapshot)
		if cfg.Type != "chroma" {
			t.Errorf("SetUpstreamKey type: got %v, want chroma", cfg.Type)
		}
	}

	// 11. Caption mode
	if !mock.wasCalled("SetCaptionMode") {
		t.Error("SetCaptionMode not called")
	} else {
		c := mock.callsFor("SetCaptionMode")[0]
		if c.Args[0] != "passthrough" {
			t.Errorf("SetCaptionMode arg: got %v, want passthrough", c.Args[0])
		}
	}

	// 12. Color grading
	if !mock.wasCalled("SetColorGrade") {
		t.Error("SetColorGrade not called")
	} else {
		c := mock.callsFor("SetColorGrade")[0]
		if c.Args[0] != "broadcast-warm" {
			t.Errorf("SetColorGrade lookName: got %v, want broadcast-warm", c.Args[0])
		}
		if c.Args[1] != float32(0.75) {
			t.Errorf("SetColorGrade intensity: got %v, want 0.75", c.Args[1])
		}
	}
	// DisableColorGrade should NOT be called when color grade is enabled
	if mock.wasCalled("DisableColorGrade") {
		t.Error("DisableColorGrade should not be called when color grade is enabled")
	}

	// 13. ST maps
	if mock.callCount("AssignSTMapSource") != 1 {
		t.Errorf("AssignSTMapSource: expected 1 call, got %d", mock.callCount("AssignSTMapSource"))
	} else {
		c := mock.callsFor("AssignSTMapSource")[0]
		if c.Args[0] != "cam1" || c.Args[1] != "barrel_correction" {
			t.Errorf("AssignSTMapSource args: got %v", c.Args)
		}
	}
	if !mock.wasCalled("AssignSTMapProgram") {
		t.Error("AssignSTMapProgram not called")
	} else {
		c := mock.callsFor("AssignSTMapProgram")[0]
		if c.Args[0] != "identity" {
			t.Errorf("AssignSTMapProgram arg: got %v, want identity", c.Args[0])
		}
	}
}

func TestApplyFullSnapshot_BestEffort(t *testing.T) {
	mock := newMockApplyTarget()
	// Inject errors on several methods
	mock.errors["Cut"] = fmt.Errorf("source not found")
	mock.errors["SetLevel"] = fmt.Errorf("channel not found")
	mock.errors["SetGraphicsLayerActive"] = fmt.Errorf("layer not found")
	mock.errors["SetDVELayout"] = fmt.Errorf("layout rejected")
	mock.errors["SetColorGrade"] = fmt.Errorf("look not found")
	mock.errors["AssignSTMapSource"] = fmt.Errorf("map not found")

	snap := populatedSnapshot()
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)

	// Should have collected warnings
	if len(warnings) == 0 {
		t.Fatal("expected warnings, got none")
	}

	// Verify specific warnings exist
	joined := strings.Join(warnings, "\n")
	expectedSubstrings := []string{
		"program source",
		"source not found",
		"audio channel",
		"channel not found",
		"graphics layer",
		"layer not found",
		"DVE layout",
		"layout rejected",
		"color grade",
		"look not found",
		"ST map source",
		"map not found",
	}
	for _, substr := range expectedSubstrings {
		if !strings.Contains(joined, substr) {
			t.Errorf("expected warning containing %q, got:\n%s", substr, joined)
		}
	}

	// Critical: methods that DON'T error should still be called.
	// SetPreview should have been called even though Cut failed.
	if !mock.wasCalled("SetPreview") {
		t.Error("SetPreview not called despite Cut failing (best-effort violated)")
	}
	// SetTransitionConfig should have been called
	if !mock.wasCalled("SetTransitionConfig") {
		t.Error("SetTransitionConfig not called (best-effort violated)")
	}
	// SetMasterLevel should have been called despite SetLevel failing
	if !mock.wasCalled("SetMasterLevel") {
		t.Error("SetMasterLevel not called (best-effort violated)")
	}
	// SetCaptionMode should have been called
	if !mock.wasCalled("SetCaptionMode") {
		t.Error("SetCaptionMode not called (best-effort violated)")
	}
	// SetFTB should have been called
	if !mock.wasCalled("SetFTB") {
		t.Error("SetFTB not called (best-effort violated)")
	}
	// Source config should still be applied
	if !mock.wasCalled("SetSourceLabel") {
		t.Error("SetSourceLabel not called (best-effort violated)")
	}
	// Upstream keys should still be applied
	if !mock.wasCalled("SetUpstreamKey") {
		t.Error("SetUpstreamKey not called (best-effort violated)")
	}
	// AssignSTMapProgram should still be called even if AssignSTMapSource failed
	if !mock.wasCalled("AssignSTMapProgram") {
		t.Error("AssignSTMapProgram not called (best-effort violated)")
	}
}

func TestApplyFullSnapshot_EmptySnapshot(t *testing.T) {
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
	}
	ctx := context.Background()

	// Must not panic
	warnings := ApplyFullSnapshot(ctx, snap, mock)

	// With empty program/preview, Cut and SetPreview should still be called
	// (empty string is a valid "no source" state)
	if !mock.wasCalled("SetTransitionConfig") {
		t.Error("SetTransitionConfig not called for empty snapshot")
	}
	if !mock.wasCalled("SetFTB") {
		t.Error("SetFTB not called for empty snapshot")
	}
	if !mock.wasCalled("SetMasterLevel") {
		t.Error("SetMasterLevel not called for empty snapshot")
	}

	// No audio channels, graphics, sources, etc — those loops should just be empty
	if mock.callCount("SetLevel") != 0 {
		t.Errorf("SetLevel: expected 0 calls, got %d", mock.callCount("SetLevel"))
	}
	if mock.callCount("SetGraphicsLayerActive") != 0 {
		t.Errorf("SetGraphicsLayerActive: expected 0 calls, got %d", mock.callCount("SetGraphicsLayerActive"))
	}
	if mock.callCount("SetSourceLabel") != 0 {
		t.Errorf("SetSourceLabel: expected 0 calls, got %d", mock.callCount("SetSourceLabel"))
	}
	if mock.callCount("SetUpstreamKey") != 0 {
		t.Errorf("SetUpstreamKey: expected 0 calls, got %d", mock.callCount("SetUpstreamKey"))
	}

	// Color grade nil — should call DisableColorGrade
	if !mock.wasCalled("DisableColorGrade") {
		t.Error("DisableColorGrade not called when ColorGrade is nil")
	}
	if mock.wasCalled("SetColorGrade") {
		t.Error("SetColorGrade should not be called when ColorGrade is nil")
	}

	// ST maps nil — should not call assign methods
	if mock.wasCalled("AssignSTMapSource") {
		t.Error("AssignSTMapSource should not be called when STMaps is nil")
	}
	if mock.wasCalled("AssignSTMapProgram") {
		t.Error("AssignSTMapProgram should not be called when STMaps is nil")
	}

	if len(warnings) > 0 {
		t.Errorf("expected no warnings for empty snapshot, got: %v", warnings)
	}
}

func TestApplyFullSnapshot_AudioChannelAllFields(t *testing.T) {
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		AudioChannels: map[string]AudioChannelSnapshot{
			"mic1": {
				Level:        -12.5,
				Trim:         5.0,
				Muted:        true,
				AFV:          false,
				PhaseInvert:  true,
				Balance:      0.3,
				HPFFrequency: 120.0,
				HPFEnabled:   true,
				EQ: [3]EQBandSnapshot{
					{Frequency: 80, Gain: -3.0, Q: 1.2, Enabled: true},
					{Frequency: 1500, Gain: 2.0, Q: 0.8, Enabled: true},
					{Frequency: 12000, Gain: -1.0, Q: 1.5, Enabled: false},
				},
				Compressor: CompressorSnapshot{
					Threshold:  -20.0,
					Ratio:      3.0,
					Attack:     5.0,
					Release:    80.0,
					MakeupGain: 4.0,
				},
				Gate: GateSnapshot{
					Threshold: -45.0,
					Range:     -70.0,
					Attack:    0.3,
					Release:   60.0,
					Hold:      30.0,
					Enabled:   true,
				},
				AudioDelayMs: 80,
			},
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	// Verify each audio method was called exactly once for "mic1"
	audioMethods := []string{
		"SetLevel", "SetTrim", "SetMuted", "SetAFV",
		"SetPhaseInvert", "SetBalance", "SetHPF",
		"SetCompressor", "SetGate", "SetAudioDelay",
	}
	for _, method := range audioMethods {
		if mock.callCount(method) != 1 {
			t.Errorf("%s: expected 1 call, got %d", method, mock.callCount(method))
		}
	}
	// 3 EQ bands
	if mock.callCount("SetEQ") != 3 {
		t.Errorf("SetEQ: expected 3 calls, got %d", mock.callCount("SetEQ"))
	}

	// Verify the actual values passed
	levelCall := mock.callsFor("SetLevel")[0]
	if levelCall.Args[0] != "mic1" || levelCall.Args[1] != -12.5 {
		t.Errorf("SetLevel args: got %v", levelCall.Args)
	}

	trimCall := mock.callsFor("SetTrim")[0]
	if trimCall.Args[0] != "mic1" || trimCall.Args[1] != 5.0 {
		t.Errorf("SetTrim args: got %v", trimCall.Args)
	}

	mutedCall := mock.callsFor("SetMuted")[0]
	if mutedCall.Args[0] != "mic1" || mutedCall.Args[1] != true {
		t.Errorf("SetMuted args: got %v", mutedCall.Args)
	}

	afvCall := mock.callsFor("SetAFV")[0]
	if afvCall.Args[0] != "mic1" || afvCall.Args[1] != false {
		t.Errorf("SetAFV args: got %v", afvCall.Args)
	}

	phaseCall := mock.callsFor("SetPhaseInvert")[0]
	if phaseCall.Args[0] != "mic1" || phaseCall.Args[1] != true {
		t.Errorf("SetPhaseInvert args: got %v", phaseCall.Args)
	}

	balCall := mock.callsFor("SetBalance")[0]
	if balCall.Args[0] != "mic1" || balCall.Args[1] != 0.3 {
		t.Errorf("SetBalance args: got %v", balCall.Args)
	}

	hpfCall := mock.callsFor("SetHPF")[0]
	if hpfCall.Args[0] != "mic1" || hpfCall.Args[1] != 120.0 || hpfCall.Args[2] != true {
		t.Errorf("SetHPF args: got %v", hpfCall.Args)
	}

	// Verify EQ bands
	eqCalls := mock.callsFor("SetEQ")
	for i, eq := range eqCalls {
		if eq.Args[0] != "mic1" {
			t.Errorf("SetEQ[%d] source: got %v, want mic1", i, eq.Args[0])
		}
		if eq.Args[1] != i {
			t.Errorf("SetEQ[%d] band: got %v, want %d", i, eq.Args[1], i)
		}
	}
	// Check band 0 values
	if eqCalls[0].Args[2] != 80.0 || eqCalls[0].Args[3] != -3.0 || eqCalls[0].Args[4] != 1.2 || eqCalls[0].Args[5] != true {
		t.Errorf("SetEQ band 0 values: got %v", eqCalls[0].Args)
	}

	compCall := mock.callsFor("SetCompressor")[0]
	if compCall.Args[0] != "mic1" ||
		compCall.Args[1] != -20.0 ||
		compCall.Args[2] != 3.0 ||
		compCall.Args[3] != 5.0 ||
		compCall.Args[4] != 80.0 ||
		compCall.Args[5] != 4.0 {
		t.Errorf("SetCompressor args: got %v", compCall.Args)
	}

	gateCall := mock.callsFor("SetGate")[0]
	if gateCall.Args[0] != "mic1" ||
		gateCall.Args[1] != -45.0 ||
		gateCall.Args[2] != -70.0 ||
		gateCall.Args[3] != 0.3 ||
		gateCall.Args[4] != 60.0 ||
		gateCall.Args[5] != 30.0 ||
		gateCall.Args[6] != true {
		t.Errorf("SetGate args: got %v", gateCall.Args)
	}

	delayCall := mock.callsFor("SetAudioDelay")[0]
	if delayCall.Args[0] != "mic1" || delayCall.Args[1] != 80 {
		t.Errorf("SetAudioDelay args: got %v", delayCall.Args)
	}
}

func TestApplyFullSnapshot_ColorGradeDisabled(t *testing.T) {
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		ColorGrade: &ColorGradeSnapshot{
			Enabled:   false,
			LookName:  "some-look",
			Intensity: 0.5,
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	// When color grade exists but is disabled, call DisableColorGrade
	if !mock.wasCalled("DisableColorGrade") {
		t.Error("DisableColorGrade not called when ColorGrade.Enabled=false")
	}
	if mock.wasCalled("SetColorGrade") {
		t.Error("SetColorGrade should not be called when ColorGrade.Enabled=false")
	}
}

func TestApplyFullSnapshot_ApplyOrder(t *testing.T) {
	mock := newMockApplyTarget()
	snap := populatedSnapshot()
	ctx := context.Background()

	ApplyFullSnapshot(ctx, snap, mock)

	// Build a method-order list by first occurrence
	seen := make(map[string]int)
	for i, c := range mock.calls {
		if _, ok := seen[c.Method]; !ok {
			seen[c.Method] = i
		}
	}

	// Verify ordering constraints (first occurrence of each group)
	orderChecks := []struct {
		before string
		after  string
	}{
		{"SetTransitionConfig", "Cut"},
		{"SetTransitionConfig", "SetPreview"},
		{"SetLevel", "Cut"},          // audio channels before program source
		{"SetMasterLevel", "Cut"},    // audio master before program source
		{"Cut", "SetPreview"},        // program before preview
		{"SetPreview", "SetFTB"},     // preview before FTB
		{"SetFTB", "SetGraphicsLayerActive"}, // FTB before graphics
		{"SetGraphicsLayerActive", "SetDVELayout"},    // graphics before DVE
		{"SetDVELayout", "SetSourceLabel"},             // DVE before source config
		{"SetSourceLabel", "SetUpstreamKey"},           // source config before keys
		{"SetUpstreamKey", "SetCaptionMode"},           // keys before captions
		{"SetCaptionMode", "SetColorGrade"},            // captions before color grade
		{"SetColorGrade", "AssignSTMapSource"},         // color grade before ST maps
	}

	for _, check := range orderChecks {
		bi, bok := seen[check.before]
		ai, aok := seen[check.after]
		if !bok {
			t.Errorf("order check: %s not called", check.before)
			continue
		}
		if !aok {
			t.Errorf("order check: %s not called", check.after)
			continue
		}
		if bi >= ai {
			t.Errorf("order violation: %s (index %d) should come before %s (index %d)",
				check.before, bi, check.after, ai)
		}
	}
}

func TestApplyFullSnapshot_STMapsSourceOnly(t *testing.T) {
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		STMaps: &STMapSnapshot{
			Sources: map[string]string{
				"cam1": "barrel",
				"cam2": "pincushion",
			},
			Program: "",
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	if mock.callCount("AssignSTMapSource") != 2 {
		t.Errorf("AssignSTMapSource: expected 2 calls, got %d", mock.callCount("AssignSTMapSource"))
	}

	// Verify sources were assigned (order may vary due to map iteration)
	calls := mock.callsFor("AssignSTMapSource")
	assigned := make(map[string]string)
	for _, c := range calls {
		assigned[c.Args[0].(string)] = c.Args[1].(string)
	}
	if assigned["cam1"] != "barrel" {
		t.Errorf("cam1 ST map: got %q, want barrel", assigned["cam1"])
	}
	if assigned["cam2"] != "pincushion" {
		t.Errorf("cam2 ST map: got %q, want pincushion", assigned["cam2"])
	}

	// AssignSTMapProgram should NOT be called when Program is empty
	if mock.wasCalled("AssignSTMapProgram") {
		t.Error("AssignSTMapProgram should not be called when Program is empty")
	}
}

func TestApplyFullSnapshot_AudioChannelDeterministicOrder(t *testing.T) {
	// Verify that audio channels are applied in sorted key order for determinism
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		AudioChannels: map[string]AudioChannelSnapshot{
			"zzz":  {Level: -1.0},
			"aaa":  {Level: -2.0},
			"mmm":  {Level: -3.0},
		},
	}
	ctx := context.Background()

	ApplyFullSnapshot(ctx, snap, mock)

	calls := mock.callsFor("SetLevel")
	if len(calls) != 3 {
		t.Fatalf("SetLevel: expected 3 calls, got %d", len(calls))
	}

	// Extract keys in call order
	var keys []string
	for _, c := range calls {
		keys = append(keys, c.Args[0].(string))
	}

	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)

	for i := range keys {
		if keys[i] != sorted[i] {
			t.Errorf("audio channels not in sorted order: got %v, want %v", keys, sorted)
			break
		}
	}
}

// --- Clip player restore tests ---

// mockClipApplyTarget embeds mockApplyTarget and additionally implements SnapshotClipApplier.
type mockClipApplyTarget struct {
	*mockApplyTarget
	clipLoads []struct {
		SlotIndex int
		ClipName  string
	}
	clipErrors map[string]error // clipName → error
}

func newMockClipApplyTarget() *mockClipApplyTarget {
	return &mockClipApplyTarget{
		mockApplyTarget: newMockApplyTarget(),
		clipErrors:      make(map[string]error),
	}
}

func (m *mockClipApplyTarget) LoadClipByName(slotIndex int, clipName string) error {
	m.clipLoads = append(m.clipLoads, struct {
		SlotIndex int
		ClipName  string
	}{slotIndex, clipName})
	if err, ok := m.clipErrors[clipName]; ok {
		return err
	}
	return nil
}

func TestApplyFullSnapshot_ClipPlayersByName(t *testing.T) {
	mock := newMockClipApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		ClipPlayerPositions: []ClipPositionSnapshot{
			{ClipName: "intro.mp4", PlayerIndex: 1, State: "playing", Speed: 1.0},
			{ClipName: "bumper.ts", PlayerIndex: 3, State: "paused", Speed: 0.5},
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	if len(mock.clipLoads) != 2 {
		t.Fatalf("expected 2 LoadClipByName calls, got %d", len(mock.clipLoads))
	}
	if mock.clipLoads[0].SlotIndex != 1 || mock.clipLoads[0].ClipName != "intro.mp4" {
		t.Errorf("clip load 0: got slot=%d name=%q", mock.clipLoads[0].SlotIndex, mock.clipLoads[0].ClipName)
	}
	if mock.clipLoads[1].SlotIndex != 3 || mock.clipLoads[1].ClipName != "bumper.ts" {
		t.Errorf("clip load 1: got slot=%d name=%q", mock.clipLoads[1].SlotIndex, mock.clipLoads[1].ClipName)
	}
}

func TestApplyFullSnapshot_ClipPlayerMissingClipWarns(t *testing.T) {
	mock := newMockClipApplyTarget()
	mock.clipErrors["missing.mp4"] = fmt.Errorf("clip not found")

	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		ClipPlayerPositions: []ClipPositionSnapshot{
			{ClipName: "intro.mp4", PlayerIndex: 1, State: "playing"},
			{ClipName: "missing.mp4", PlayerIndex: 2, State: "paused"},
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)

	// Should have 1 warning for the missing clip
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "missing.mp4") {
		t.Errorf("warning should mention clip name, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "clip not found") {
		t.Errorf("warning should mention error, got: %s", warnings[0])
	}

	// Both clips should have been attempted (best-effort)
	if len(mock.clipLoads) != 2 {
		t.Errorf("expected 2 LoadClipByName calls (best-effort), got %d", len(mock.clipLoads))
	}
}

func TestApplyFullSnapshot_ClipPlayerSkipsWithoutApplier(t *testing.T) {
	// Base mockApplyTarget does NOT implement SnapshotClipApplier.
	mock := newMockApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		ClipPlayerPositions: []ClipPositionSnapshot{
			{ClipName: "intro.mp4", PlayerIndex: 1, State: "playing"},
		},
	}
	ctx := context.Background()

	// Should not panic — just skip clip restoration gracefully.
	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings when no clip applier, got: %v", warnings)
	}
}

func TestApplyFullSnapshot_ClipPlayerSkipsEmptyNames(t *testing.T) {
	mock := newMockClipApplyTarget()
	snap := FullStateSnapshot{
		Version:    "1",
		CapturedAt: 1234567890,
		ClipPlayerPositions: []ClipPositionSnapshot{
			{ClipName: "", PlayerIndex: 1, State: "empty"},
			{ClipName: "intro.mp4", PlayerIndex: 2, State: "playing"},
		},
	}
	ctx := context.Background()

	warnings := ApplyFullSnapshot(ctx, snap, mock)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}

	// Only the non-empty name should have been loaded
	if len(mock.clipLoads) != 1 {
		t.Fatalf("expected 1 LoadClipByName call, got %d", len(mock.clipLoads))
	}
	if mock.clipLoads[0].ClipName != "intro.mp4" {
		t.Errorf("expected intro.mp4, got %q", mock.clipLoads[0].ClipName)
	}
}
