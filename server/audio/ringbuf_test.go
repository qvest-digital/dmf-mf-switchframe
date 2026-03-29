package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPCMRingBuffer_PushPop_Basic(t *testing.T) {
	rb := NewPCMRingBuffer(8)
	rb.Push([]float32{1, 2, 3, 4})
	assert.Equal(t, 4, rb.Len())

	out := rb.Pop(4)
	require.NotNil(t, out)
	assert.Equal(t, []float32{1, 2, 3, 4}, out)
	assert.Equal(t, 0, rb.Len())
}

func TestPCMRingBuffer_PushPop_MultipleChunks(t *testing.T) {
	rb := NewPCMRingBuffer(16)
	rb.Push([]float32{1, 2, 3})
	rb.Push([]float32{4, 5})
	assert.Equal(t, 5, rb.Len())

	out := rb.Pop(4)
	assert.Equal(t, []float32{1, 2, 3, 4}, out)
	assert.Equal(t, 1, rb.Len())

	out = rb.Pop(1)
	assert.Equal(t, []float32{5}, out)
}

func TestPCMRingBuffer_Overflow_DropsOldest(t *testing.T) {
	// Use a buffer just big enough to test overflow.
	// Capacity 2048 (minimum), push 2048 then 1024 more.
	rb := &PCMRingBuffer{
		buf: make([]float32, 6),
		cap: 6,
	}
	rb.Push([]float32{1, 2, 3, 4, 5, 6}) // fill
	rb.Push([]float32{7, 8})             // overflow, drops 1,2

	out := rb.Pop(6)
	assert.Equal(t, []float32{3, 4, 5, 6, 7, 8}, out)
}

func TestPCMRingBuffer_PopEmpty_ReturnsSilence(t *testing.T) {
	rb := NewPCMRingBuffer(2048)
	rb.Push([]float32{10, 20, 30, 40})
	out1 := rb.Pop(4)
	assert.Equal(t, []float32{10, 20, 30, 40}, out1)

	// Empty buffer returns silence (zeros), not freeze-repeat
	out2 := rb.Pop(4)
	assert.Equal(t, []float32{0, 0, 0, 0}, out2)
}

func TestPCMRingBuffer_PopNeverPushed_ReturnsSilence(t *testing.T) {
	rb := NewPCMRingBuffer(2048)
	out := rb.Pop(4)
	assert.Equal(t, []float32{0, 0, 0, 0}, out)
}

func TestPCMRingBuffer_DeepCopy(t *testing.T) {
	rb := NewPCMRingBuffer(8)
	input := []float32{1, 2, 3, 4}
	rb.Push(input)
	input[0] = 999
	out := rb.Pop(4)
	assert.Equal(t, float32(1), out[0])
}

func TestPCMRingBuffer_WrapAround(t *testing.T) {
	rb := NewPCMRingBuffer(6)
	rb.Push([]float32{1, 2, 3, 4, 5, 6})
	out := rb.Pop(6)
	assert.Equal(t, []float32{1, 2, 3, 4, 5, 6}, out)

	rb.Push([]float32{7, 8, 9, 10, 11, 12})
	out = rb.Pop(6)
	assert.Equal(t, []float32{7, 8, 9, 10, 11, 12}, out)
}

func TestPCMRingBuffer_VariablePushFixedPop(t *testing.T) {
	rb := NewPCMRingBuffer(4096)

	frame1 := make([]float32, 1115)
	for i := range frame1 {
		frame1[i] = float32(i)
	}
	rb.Push(frame1)
	assert.Equal(t, 1115, rb.Len())

	out := rb.Pop(1024)
	require.Len(t, out, 1024)
	assert.Equal(t, float32(0), out[0])
	assert.Equal(t, float32(1023), out[1023])
	assert.Equal(t, 91, rb.Len())

	frame2 := make([]float32, 1115)
	for i := range frame2 {
		frame2[i] = float32(i + 2000)
	}
	rb.Push(frame2)
	assert.Equal(t, 91+1115, rb.Len())

	out = rb.Pop(1024)
	require.Len(t, out, 1024)
	assert.Equal(t, float32(1024), out[0])
	assert.Equal(t, 1206-1024, rb.Len())
}

func TestPCMRingBuffer_Reset(t *testing.T) {
	rb := NewPCMRingBuffer(2048)
	rb.Push([]float32{1, 2, 3, 4})
	rb.Pop(4)

	rb.Reset()
	assert.Equal(t, 0, rb.Len())

	// After reset, empty pop returns silence
	out := rb.Pop(4)
	assert.Equal(t, []float32{0, 0, 0, 0}, out)
}

func TestPCMRingBuffer_PopDriftCompensated_AtTarget(t *testing.T) {
	// When depth ≈ target, should behave like Pop.
	rb := NewPCMRingBuffer(20480)
	channels := 2
	target := 2 * 1024 * channels // 4096 samples

	fill := make([]float32, target)
	for i := range fill {
		fill[i] = float32(i) / float32(target)
	}
	rb.Push(fill)

	out := rb.PopDriftCompensated(2048, target, channels)
	require.Len(t, out, 2048)
	assert.InDelta(t, 2048, rb.Len(), 2, "depth should be near target minus read")
}

func TestPCMRingBuffer_PopDriftCompensated_OverTarget(t *testing.T) {
	rb := NewPCMRingBuffer(20480)
	channels := 2
	target := 2 * 1024 * channels
	requestN := 1024 * channels

	fill := make([]float32, target*3)
	for i := range fill {
		fill[i] = float32(i)
	}
	rb.Push(fill)
	depthBefore := rb.Len()

	out := rb.PopDriftCompensated(requestN, target, channels)
	require.Len(t, out, requestN, "must return exactly requested count")

	consumed := depthBefore - rb.Len()
	assert.Greater(t, consumed, requestN, "should consume more than requested when over target")
}

func TestPCMRingBuffer_PopDriftCompensated_UnderTarget(t *testing.T) {
	rb := NewPCMRingBuffer(20480)
	channels := 2
	target := 2 * 1024 * channels
	requestN := 1024 * channels

	fill := make([]float32, target/4)
	for i := range fill {
		fill[i] = 0.5
	}
	rb.Push(fill)
	depthBefore := rb.Len()

	out := rb.PopDriftCompensated(requestN, target, channels)
	require.Len(t, out, requestN, "must always return exactly requested count")

	consumed := depthBefore - rb.Len()
	assert.Less(t, consumed, requestN, "should consume fewer than requested when under target")
}

func TestPCMRingBuffer_PopDriftCompensated_Empty(t *testing.T) {
	rb := NewPCMRingBuffer(20480)
	out := rb.PopDriftCompensated(2048, 4096, 2)
	require.Len(t, out, 2048)
	for _, s := range out {
		assert.Equal(t, float32(0), s)
	}
}

func TestPCMRingBuffer_PopDriftCompensated_Convergence(t *testing.T) {
	rb := NewPCMRingBuffer(20480)
	channels := 2
	target := 2 * 1024 * channels
	readN := 1024 * channels
	pushN := 1025 * channels // slight overshoot = clock drift

	seed := make([]float32, target)
	for i := range seed {
		seed[i] = 0.3
	}
	rb.Push(seed)

	pushBuf := make([]float32, pushN)
	for i := range pushBuf {
		pushBuf[i] = 0.3
	}

	for i := 0; i < 500; i++ {
		rb.Push(pushBuf)
		_ = rb.PopDriftCompensated(readN, target, channels)
	}

	depth := rb.Len()
	assert.InDelta(t, target, depth, float64(target),
		"depth should stay near target, got %d (target %d)", depth, target)
}
