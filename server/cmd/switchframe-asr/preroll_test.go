package main

import "testing"

func sampleRamp(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(i)
	}
	return out
}

func TestPrerollBuffer_NewIsEmpty(t *testing.T) {
	r := newPrerollBuffer(100)
	if got := r.Snapshot(); len(got) != 0 {
		t.Errorf("snapshot of empty ring should be empty, got %d samples", len(got))
	}
}

func TestPrerollBuffer_BelowCapacityReturnsAll(t *testing.T) {
	r := newPrerollBuffer(100)
	r.Write(sampleRamp(40))
	got := r.Snapshot()
	if len(got) != 40 {
		t.Fatalf("got %d samples, want 40", len(got))
	}
	for i, v := range got {
		if v != float32(i) {
			t.Errorf("got[%d] = %f, want %f", i, v, float32(i))
		}
	}
}

func TestPrerollBuffer_AtCapacityReturnsAll(t *testing.T) {
	r := newPrerollBuffer(100)
	r.Write(sampleRamp(100))
	got := r.Snapshot()
	if len(got) != 100 {
		t.Fatalf("got %d samples, want 100", len(got))
	}
	for i, v := range got {
		if v != float32(i) {
			t.Errorf("got[%d] = %f, want %f", i, v, float32(i))
		}
	}
}

func TestPrerollBuffer_OverflowKeepsLastN(t *testing.T) {
	r := newPrerollBuffer(10)
	r.Write(sampleRamp(25))
	got := r.Snapshot()
	if len(got) != 10 {
		t.Fatalf("got %d samples, want 10", len(got))
	}
	// Last 10 of 0..24 are 15..24.
	for i, v := range got {
		want := float32(15 + i)
		if v != want {
			t.Errorf("got[%d] = %f, want %f", i, v, want)
		}
	}
}

func TestPrerollBuffer_MultipleWritesAccumulate(t *testing.T) {
	r := newPrerollBuffer(10)
	r.Write(sampleRamp(5))             // 0..4
	r.Write([]float32{100, 101, 102}) // 100..102
	got := r.Snapshot()
	want := []float32{0, 1, 2, 3, 4, 100, 101, 102}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d (%v)", len(got), len(want), got)
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("got[%d] = %f, want %f", i, v, want[i])
		}
	}
}

func TestPrerollBuffer_WriteLargerThanCapacity(t *testing.T) {
	r := newPrerollBuffer(5)
	r.Write(sampleRamp(20))
	got := r.Snapshot()
	if len(got) != 5 {
		t.Fatalf("got %d samples, want 5", len(got))
	}
	// Last 5 of 0..19 = 15..19.
	want := []float32{15, 16, 17, 18, 19}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("got[%d] = %f, want %f", i, v, want[i])
		}
	}
}

func TestPrerollBuffer_SnapshotIsCopy(t *testing.T) {
	r := newPrerollBuffer(10)
	r.Write(sampleRamp(5))
	got := r.Snapshot()
	got[0] = 999
	got2 := r.Snapshot()
	if got2[0] != 0 {
		t.Errorf("Snapshot must return a copy; mutation leaked, got %f", got2[0])
	}
}

func TestPrerollBuffer_Reset(t *testing.T) {
	r := newPrerollBuffer(10)
	r.Write(sampleRamp(7))
	r.Reset()
	if got := r.Snapshot(); len(got) != 0 {
		t.Errorf("after reset, snapshot should be empty, got %d samples", len(got))
	}
}
