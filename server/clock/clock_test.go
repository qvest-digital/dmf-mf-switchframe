package clock

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestNow_ReturnsReasonablePTPTime verifies that Now() returns a value within
// 100ms of the wall clock's UnixMicro value.
func TestNow_ReturnsReasonablePTPTime(t *testing.T) {
	before := time.Now().UnixMicro()
	got := Now()
	after := time.Now().UnixMicro()

	const toleranceMicro = 100_000 // 100ms

	if got < before-toleranceMicro || got > after+toleranceMicro {
		t.Errorf("Now() = %d, want in range [%d, %d] (±100ms of wall clock)",
			got, before-toleranceMicro, after+toleranceMicro)
	}
}

// TestNow_Monotonic verifies that successive calls to Now() return
// non-decreasing (monotonically increasing) values.
func TestNow_Monotonic(t *testing.T) {
	const iterations = 100
	prev := Now()
	for i := 0; i < iterations; i++ {
		curr := Now()
		if curr < prev {
			t.Errorf("Now() went backward: call %d returned %d, previous was %d", i+1, curr, prev)
		}
		prev = curr
	}
}

// TestGetSyncState_InitialStateOrMonitored verifies that the default sync
// state is either the conservative initializing default (before the
// sampler has collected a full window) or a monitored reading. Both must
// populate Source with a non-empty string so downstream consumers can
// switch on it — a hardcoded "ntp" was the pre-BUG-057 bug.
func TestGetSyncState_InitialStateOrMonitored(t *testing.T) {
	state := GetSyncState()
	if state.Source == "" {
		t.Error("GetSyncState().Source is empty — must identify the detection mechanism")
	}
	switch state.Source {
	case "systemclock-initializing", "systemclock-monitored":
		// ok
	default:
		t.Errorf("GetSyncState().Source = %q, want one of systemclock-initializing | systemclock-monitored",
			state.Source)
	}
}

// TestGetSyncState_MockableForTests confirms the test seam — tests can
// install a fixture result via SetGetSyncStateFn without poking atomic
// package state. Essential for downstream tests that assert broadcast
// behavior on specific (Synchronized, Source, WindowDriftUs) triples.
func TestGetSyncState_MockableForTests(t *testing.T) {
	want := SyncState{Synchronized: false, WindowDriftUs: 123456, Source: "test-mock"}
	prev := SetGetSyncStateFn(func() SyncState { return want })
	defer SetGetSyncStateFn(prev)

	got := GetSyncState()
	if got != want {
		t.Errorf("GetSyncState() after mock install = %+v, want %+v", got, want)
	}
}

// TestClockSyncState_DegradedOnPmcAbsent — AC#7. pmc-based detection is
// deferred to Phase B (the Resolution section of the item tracks this).
// In Phase A the fallback detector is always used; Source MUST NOT claim
// "ptp" because we have no positive PTP evidence.
func TestClockSyncState_DegradedOnPmcAbsent(t *testing.T) {
	state := GetSyncState()
	if state.Source == "ptp" {
		t.Errorf("GetSyncState().Source = %q; pmc integration is Phase B, Source must not claim ptp yet",
			state.Source)
	}
}

// TestSyncState_JSONRoundTrip verifies that SyncState serializes to the
// expected JSON field names and round-trips correctly.
func TestSyncState_JSONRoundTrip(t *testing.T) {
	original := SyncState{
		Synchronized:  true,
		WindowDriftUs: -42,
		Source:        "ptp",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(SyncState): %v", err)
	}

	// Verify field names in JSON.
	jsonStr := string(data)
	for _, field := range []string{`"synchronized"`, `"windowDriftUs"`, `"source"`} {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("JSON %q missing expected field %s", jsonStr, field)
		}
	}

	var decoded SyncState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(SyncState): %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, original)
	}
}
