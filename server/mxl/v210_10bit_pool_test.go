package mxl

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests for finding #41: the 10-bit video path allocated a fresh multi-MB
// buffer per frame on both hot paths — Source.processVideoGrain
// (V210→YUV422P10) and Writer.WriteVideo10bit (YUV422P10→V210) — while the
// 8-bit equivalents were deliberately pooled (v210Buffers.ensureSize and the
// Writer's 4-slot v210Bufs ring).

// make10bitYUV builds a YUV422P10LE frame (w*h*4 bytes) with neutral content.
func make10bitYUV(w, h int) []byte {
	return make([]byte, w*h*4)
}

// TestWriteVideo10bit_NoPerFrameAllocation verifies that the 10-bit write
// path reuses the Writer's V210 ring instead of allocating a fresh V210
// buffer (~5.5 MB at 1080p) per frame. The only allowed steady-state
// allocation is the small v210Frame published to the ticker.
func TestWriteVideo10bit_NoPerFrameAllocation(t *testing.T) {
	w, h := 12, 4
	writer := NewWriter(WriterConfig{Width: w, Height: h})
	yuv := make10bitYUV(w, h)

	// Warm up: allocate the ring.
	for i := 0; i < 5; i++ {
		writer.WriteVideo10bit(yuv, w, h, int64(i))
	}

	allocs := testing.AllocsPerRun(100, func() {
		writer.WriteVideo10bit(yuv, w, h, 0)
	})
	require.LessOrEqualf(t, allocs, 1.0,
		"WriteVideo10bit should only allocate the published v210Frame per call, got %.1f allocs/run", allocs)
}

// TestWriteVideo10bit_UsesRingSlots verifies the 10-bit path participates in
// the same in-flight ring coordination as the 8-bit path: frames land in
// ring slots (slot >= 0), successive frames use different slots, and a slot
// claimed by the ticker is dropped rather than overwritten mid-cgo-copy.
func TestWriteVideo10bit_UsesRingSlots(t *testing.T) {
	w, h := 12, 4
	writer := NewWriter(WriterConfig{Width: w, Height: h})
	yuv := make10bitYUV(w, h)

	writer.WriteVideo10bit(yuv, w, h, 0)
	fA := writer.latestV210.Load()
	require.NotNil(t, fA)
	require.GreaterOrEqual(t, fA.slot, 0,
		"10-bit frames should use ring slots, not standalone allocations")

	writer.WriteVideo10bit(yuv, w, h, 1)
	fB := writer.latestV210.Load()
	require.NotNil(t, fB)
	require.NotEqual(t, fA.slot, fB.slot, "successive frames must land in distinct ring slots")

	// Claim the slot the producer would use next; the write must drop.
	next := (fB.slot + 1) % len(writer.v210Bufs)
	writer.v210InFlight.Store(int64(next))
	dropsBefore := writer.VideoDrops()
	writer.WriteVideo10bit(yuv, w, h, 2)
	require.Equal(t, dropsBefore+1, writer.VideoDrops(),
		"writing into an in-flight slot must drop the frame")
	require.Same(t, fB, writer.latestV210.Load(), "dropped frame must not replace the latest frame")
	writer.v210InFlight.Store(-1)
}

// TestProcessVideoGrain_Lossless10bit_NoPerFrameAllocation verifies that the
// source-side V210→YUV422P10 conversion reuses a pooled buffer instead of
// allocating ~8.3 MB (at 1080p) per grain.
func TestProcessVideoGrain_Lossless10bit_NoPerFrameAllocation(t *testing.T) {
	const w, h = 12, 4
	src := NewSource(SourceConfig{
		FlowName:      "cam1",
		Width:         w,
		Height:        h,
		Lossless10bit: true,
		// No OnRawVideo/Relay: isolates the conversion itself.
	})

	grain := VideoGrain{
		V210:     makeV210Frame10bit(w, h),
		Width:    w,
		Height:   h,
		PTS:      1,
		ReadTime: time.Now(),
	}

	// Warm up: allocates the pooled conversion buffer and sets the epoch.
	src.processVideoGrain(grain)

	allocs := testing.AllocsPerRun(100, func() {
		src.processVideoGrain(grain)
	})
	require.LessOrEqualf(t, allocs, 0.0,
		"processVideoGrain (10-bit, no sinks) should not allocate per grain, got %.1f allocs/run", allocs)
}

// TestV210ToYUV422P10Into_MatchesAllocatingVariant verifies the buffer-reuse
// variant is bit-identical to the allocating reference, and rejects
// undersized output buffers instead of writing out of bounds.
func TestV210ToYUV422P10Into_MatchesAllocatingVariant(t *testing.T) {
	for _, dims := range []struct{ w, h int }{{12, 4}, {16, 2}, {14, 2}, {48, 16}} {
		v210 := make([]byte, V210LineStride(dims.w)*dims.h)
		for i := range v210 {
			v210[i] = byte((i*31 + 7) & 0xff)
		}
		stride := V210LineStride(dims.w)

		want, err := V210ToYUV422P10(v210, stride, dims.w, dims.h)
		require.NoError(t, err)

		got := make([]byte, dims.w*dims.h*4)
		require.NoError(t, V210ToYUV422P10Into(v210, got, stride, dims.w, dims.h))
		require.Equal(t, want, got, "%dx%d", dims.w, dims.h)

		// Undersized output must be rejected.
		short := make([]byte, dims.w*dims.h*4-1)
		require.Error(t, V210ToYUV422P10Into(v210, short, stride, dims.w, dims.h))
	}
}

// TestYUV422P10ToV210Into_MatchesAllocatingVariant is the pack-direction
// equivalent of the test above.
func TestYUV422P10ToV210Into_MatchesAllocatingVariant(t *testing.T) {
	for _, dims := range []struct{ w, h int }{{12, 4}, {48, 16}} {
		yuv := make([]byte, dims.w*dims.h*4)
		for i := 0; i < len(yuv); i += 2 {
			v := uint16((i*13 + 5) & 0x3FF)
			yuv[i] = byte(v)
			yuv[i+1] = byte(v >> 8)
		}

		want, err := YUV422P10ToV210(yuv, dims.w, dims.h)
		require.NoError(t, err)

		got := make([]byte, V210LineStride(dims.w)*dims.h)
		require.NoError(t, YUV422P10ToV210Into(yuv, got, dims.w, dims.h))
		require.Equal(t, want, got, "%dx%d", dims.w, dims.h)

		// Undersized output must be rejected.
		short := make([]byte, V210LineStride(dims.w)*dims.h-1)
		require.Error(t, YUV422P10ToV210Into(yuv, short, dims.w, dims.h))
	}
}
