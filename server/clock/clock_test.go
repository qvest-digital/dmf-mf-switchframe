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

// TestGetSyncState_DefaultNTP verifies that the default sync state reports
// NTP-disciplined system clock.
func TestGetSyncState_DefaultNTP(t *testing.T) {
	state := GetSyncState()
	if !state.Synchronized {
		t.Error("GetSyncState().Synchronized = false, want true")
	}
	if state.Source != "ntp" {
		t.Errorf("GetSyncState().Source = %q, want %q", state.Source, "ntp")
	}
	if state.OffsetUs != 0 {
		t.Errorf("GetSyncState().OffsetUs = %d, want 0", state.OffsetUs)
	}
}

// TestSyncState_JSONRoundTrip verifies that SyncState serializes to the
// expected JSON field names and round-trips correctly.
func TestSyncState_JSONRoundTrip(t *testing.T) {
	original := SyncState{
		Synchronized: true,
		OffsetUs:     -42,
		Source:       "ptp",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(SyncState): %v", err)
	}

	// Verify field names in JSON.
	jsonStr := string(data)
	for _, field := range []string{`"synchronized"`, `"offsetUs"`, `"source"`} {
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
