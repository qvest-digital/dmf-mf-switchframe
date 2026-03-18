package audio

import (
	"testing"
)

func TestPCMRingBuffer_PushPop_FIFO(t *testing.T) {
	rb := NewPCMRingBuffer(8)

	// Push 3 distinct frames.
	frame0 := []float32{1.0, 2.0}
	frame1 := []float32{3.0, 4.0}
	frame2 := []float32{5.0, 6.0}
	rb.Push(frame0)
	rb.Push(frame1)
	rb.Push(frame2)

	if rb.Len() != 3 {
		t.Fatalf("Len: got %d, want 3", rb.Len())
	}

	// Pop should return FIFO order.
	got0 := rb.Pop()
	got1 := rb.Pop()
	got2 := rb.Pop()

	assertSliceEqual(t, got0, frame0)
	assertSliceEqual(t, got1, frame1)
	assertSliceEqual(t, got2, frame2)

	if rb.Len() != 0 {
		t.Fatalf("Len after drain: got %d, want 0", rb.Len())
	}
}

func TestPCMRingBuffer_Overflow_DropsOldest(t *testing.T) {
	rb := NewPCMRingBuffer(3)

	// Push 4 frames into a cap-3 buffer. Oldest should be dropped.
	rb.Push([]float32{1.0})
	rb.Push([]float32{2.0})
	rb.Push([]float32{3.0})
	rb.Push([]float32{4.0}) // drops {1.0}

	if rb.Len() != 3 {
		t.Fatalf("Len: got %d, want 3", rb.Len())
	}

	// Should get frames 2, 3, 4 (oldest dropped).
	assertSliceEqual(t, rb.Pop(), []float32{2.0})
	assertSliceEqual(t, rb.Pop(), []float32{3.0})
	assertSliceEqual(t, rb.Pop(), []float32{4.0})
}

func TestPCMRingBuffer_PopEmpty_FreezeRepeat(t *testing.T) {
	rb := NewPCMRingBuffer(4)

	rb.Push([]float32{10.0, 20.0})
	got := rb.Pop()
	assertSliceEqual(t, got, []float32{10.0, 20.0})

	// Buffer is now empty. Pop should return a copy of the last popped frame.
	freeze1 := rb.Pop()
	assertSliceEqual(t, freeze1, []float32{10.0, 20.0})

	freeze2 := rb.Pop()
	assertSliceEqual(t, freeze2, []float32{10.0, 20.0})

	// Freeze-repeat frames must be independent copies.
	if len(freeze1) > 0 && len(freeze2) > 0 {
		freeze1[0] = 999.0
		if freeze2[0] == 999.0 {
			t.Fatal("freeze-repeat returned aliased slices")
		}
	}
}

func TestPCMRingBuffer_PopNeverPushed_ReturnsNil(t *testing.T) {
	rb := NewPCMRingBuffer(4)

	got := rb.Pop()
	if got != nil {
		t.Fatalf("Pop on never-pushed buffer: got %v, want nil", got)
	}
}

func TestPCMRingBuffer_Reset_ClearsButKeepsLast(t *testing.T) {
	rb := NewPCMRingBuffer(4)

	rb.Push([]float32{1.0, 2.0})
	rb.Push([]float32{3.0, 4.0})
	_ = rb.Pop() // pops {1.0, 2.0}  -> lastFrame = {1.0, 2.0}
	_ = rb.Pop() // pops {3.0, 4.0}  -> lastFrame = {3.0, 4.0}

	rb.Reset()

	if rb.Len() != 0 {
		t.Fatalf("Len after Reset: got %d, want 0", rb.Len())
	}

	// Pop should still return freeze-repeat of the last popped frame.
	freeze := rb.Pop()
	assertSliceEqual(t, freeze, []float32{3.0, 4.0})
}

func TestPCMRingBuffer_DeepCopy(t *testing.T) {
	rb := NewPCMRingBuffer(4)

	original := []float32{100.0, 200.0, 300.0}
	rb.Push(original)

	// Mutate the original after push.
	original[0] = -1.0
	original[1] = -2.0
	original[2] = -3.0

	// Ring buffer should have the values at time of push, not the mutated ones.
	got := rb.Pop()
	assertSliceEqual(t, got, []float32{100.0, 200.0, 300.0})

	// Also verify the popped slice is independent from the ring buffer internals.
	got[0] = 0.0
	// Push and pop another frame to exercise the same slot.
	rb.Push([]float32{400.0})
	got2 := rb.Pop()
	assertSliceEqual(t, got2, []float32{400.0})
}

func TestPCMRingBuffer_WrapAround(t *testing.T) {
	// Verify correct behavior when head/tail wrap around the circular buffer.
	rb := NewPCMRingBuffer(3)

	// Fill and drain twice to force wrap-around.
	for cycle := 0; cycle < 2; cycle++ {
		rb.Push([]float32{float32(cycle*10 + 1)})
		rb.Push([]float32{float32(cycle*10 + 2)})
		rb.Push([]float32{float32(cycle*10 + 3)})

		assertSliceEqual(t, rb.Pop(), []float32{float32(cycle*10 + 1)})
		assertSliceEqual(t, rb.Pop(), []float32{float32(cycle*10 + 2)})
		assertSliceEqual(t, rb.Pop(), []float32{float32(cycle*10 + 3)})
	}
}

func TestPCMRingBuffer_OverflowPreservesSlotReuse(t *testing.T) {
	// Ensure that after overflow, previously allocated slot slices are reused
	// (no new allocation if capacity is sufficient).
	rb := NewPCMRingBuffer(2)

	// Push frames of same size to prime slots.
	rb.Push([]float32{1.0, 2.0})
	rb.Push([]float32{3.0, 4.0})

	// Overflow: push a third frame of same size. Oldest slot gets reused.
	rb.Push([]float32{5.0, 6.0})

	assertSliceEqual(t, rb.Pop(), []float32{3.0, 4.0})
	assertSliceEqual(t, rb.Pop(), []float32{5.0, 6.0})
}

func TestPCMRingBuffer_VariableFrameSizes(t *testing.T) {
	// Frames can have different sizes (e.g., resampled audio producing variable output).
	rb := NewPCMRingBuffer(4)

	rb.Push([]float32{1.0})
	rb.Push([]float32{2.0, 3.0, 4.0})
	rb.Push([]float32{5.0, 6.0})

	assertSliceEqual(t, rb.Pop(), []float32{1.0})
	assertSliceEqual(t, rb.Pop(), []float32{2.0, 3.0, 4.0})
	assertSliceEqual(t, rb.Pop(), []float32{5.0, 6.0})
}

func TestPCMRingBuffer_PopReturnsCopy(t *testing.T) {
	// Verify that Pop returns a copy, not internal buffer memory.
	rb := NewPCMRingBuffer(4)

	rb.Push([]float32{10.0, 20.0})
	got := rb.Pop()

	// Mutate returned slice.
	got[0] = -999.0

	// Push and pop from the same slot should not see the mutation.
	rb.Push([]float32{30.0, 40.0})
	got2 := rb.Pop()
	assertSliceEqual(t, got2, []float32{30.0, 40.0})
}

// assertSliceEqual is a test helper that compares two float32 slices.
func assertSliceEqual(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length: got %d, want %d (got=%v, want=%v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice[%d]: got %v, want %v (full got=%v, want=%v)", i, got[i], want[i], got, want)
		}
	}
}
