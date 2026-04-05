package clock

import (
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
