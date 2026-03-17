package switcher

import (
	"testing"
)

// BenchmarkFramePool_AcquireRelease measures the raw cost of pool
// acquire/release under no contention. This is the per-frame overhead
// added by DecodeInto's pre-acquire pattern.
func BenchmarkFramePool_AcquireRelease(b *testing.B) {
	fp := NewFramePool(96, 1920, 1080)
	defer fp.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := fp.Acquire()
		fp.Release(buf)
	}
}

// BenchmarkFramePool_AcquireMiss measures the cost of a pool miss
// (make + memclr of 3.1MB). This is what happens when the pool is empty.
func BenchmarkFramePool_AcquireMiss(b *testing.B) {
	// Empty pool — every acquire is a miss
	fp := NewFramePool(0, 1920, 1080)
	defer fp.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := fp.Acquire()
		_ = buf
		// Don't release — simulates the miss path where buffer becomes GC garbage
	}
}

// BenchmarkDecodeLoop_OldPath simulates the pre-DecodeInto sourceDecoder:
// decoder.Decode() returns a fresh allocation, then we copy into a pool buffer.
// Two allocations, two copies per frame.
func BenchmarkDecodeLoop_OldPath(b *testing.B) {
	fp := NewFramePool(96, 1920, 1080)
	defer fp.Close()
	yuvSize := 1920 * 1080 * 3 / 2
	// Simulate decoder output (fresh allocation each time)
	decoderOutput := make([]byte, yuvSize)
	for i := range decoderOutput {
		decoderOutput[i] = byte(i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Simulate adoptOrCopy: decoder allocates fresh
		decoded := make([]byte, yuvSize)
		copy(decoded, decoderOutput)

		// Then sourceDecoder copies into pool buffer
		buf := fp.Acquire()
		copy(buf, decoded[:yuvSize])

		// Frame goes through pipeline, eventually released
		fp.Release(buf)
	}
}

// BenchmarkDecodeLoop_DecodeIntoPath simulates the DecodeInto path:
// pre-acquire pool buffer, decoder writes directly into it.
// One pool acquire, one copy (decoder internal → pool buffer), zero extra alloc on hit.
func BenchmarkDecodeLoop_DecodeIntoPath(b *testing.B) {
	fp := NewFramePool(96, 1920, 1080)
	defer fp.Close()
	yuvSize := 1920 * 1080 * 3 / 2
	// Simulate decoder's internal buffer
	decoderInternal := make([]byte, yuvSize)
	for i := range decoderInternal {
		decoderInternal[i] = byte(i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Pre-acquire pool buffer
		poolBuf := fp.Acquire()

		// Simulate DecodeInto: decoder copies directly into pool buffer
		copy(poolBuf, decoderInternal[:yuvSize])

		// No second copy needed — poolBuf IS the frame buffer
		// Frame goes through pipeline, eventually released
		fp.Release(poolBuf)
	}
}

// BenchmarkDecodeLoop_DecodeIntoPath_PoolMiss simulates DecodeInto
// when the pool is empty (worst case).
func BenchmarkDecodeLoop_DecodeIntoPath_PoolMiss(b *testing.B) {
	// Pool with 0 pre-allocated = every acquire is a miss
	fp := NewFramePool(0, 1920, 1080)
	defer fp.Close()
	yuvSize := 1920 * 1080 * 3 / 2
	decoderInternal := make([]byte, yuvSize)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Pool miss: make(3.1MB) + memclr
		poolBuf := fp.Acquire()
		copy(poolBuf, decoderInternal[:yuvSize])
		// Don't release — simulates buffer being held by pipeline
	}
}

// BenchmarkDecodeLoop_OldPath_PoolMiss simulates the old path when
// pool is empty (both decoder alloc AND pool alloc miss).
func BenchmarkDecodeLoop_OldPath_PoolMiss(b *testing.B) {
	fp := NewFramePool(0, 1920, 1080)
	defer fp.Close()
	yuvSize := 1920 * 1080 * 3 / 2
	decoderOutput := make([]byte, yuvSize)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// adoptOrCopy: make + copy
		decoded := make([]byte, yuvSize)
		copy(decoded, decoderOutput)

		// Pool miss: make + copy
		buf := fp.Acquire()
		copy(buf, decoded[:yuvSize])
		// Don't release
	}
}

// BenchmarkFRCIngest_WithSceneChange measures FRC ingest cost with
// scene change detection enabled (the old behavior for all sources).
func BenchmarkFRCIngest_WithSceneChange(b *testing.B) {
	frc := newFRCSource(FRCMCFI, 3000)
	yuvSize := 1920 * 1080 * 3 / 2

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pf := &ProcessingFrame{
			YUV:    make([]byte, yuvSize),
			Width:  1920,
			Height: 1080,
			PTS:    int64(i * 3000),
		}
		pf.SetRefs(2)
		frc.ingest(pf)
	}
	frc.reset()
}

// BenchmarkFRCIngest_Nearest measures FRC ingest cost at FRCNearest
// (skips scene change detection — our optimization).
func BenchmarkFRCIngest_Nearest(b *testing.B) {
	frc := newFRCSource(FRCNearest, 3000)
	yuvSize := 1920 * 1080 * 3 / 2

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pf := &ProcessingFrame{
			YUV:    make([]byte, yuvSize),
			Width:  1920,
			Height: 1080,
			PTS:    int64(i * 3000),
		}
		pf.SetRefs(2)
		frc.ingest(pf)
	}
	frc.reset()
}

// BenchmarkSRTCallback_OldSharedBuffer simulates the old SRT OnRawVideo path:
// single make() shared between pipeline and relay channels.
func BenchmarkSRTCallback_OldSharedBuffer(b *testing.B) {
	yuvSize := 1920 * 1080 * 3 / 2
	srcBuf := make([]byte, yuvSize) // simulates d.videoGoBuf

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Old path: one make + copy, shared by both consumers
		yuvCopy := make([]byte, len(srcBuf))
		copy(yuvCopy, srcBuf)
		_ = yuvCopy
	}
}

// BenchmarkSRTCallback_PoolSeparateBuffers simulates the new SRT path:
// two pool acquires (pipeline + relay), no make() on hit.
func BenchmarkSRTCallback_PoolSeparateBuffers(b *testing.B) {
	fp := NewFramePool(96, 1920, 1080)
	defer fp.Close()
	yuvSize := 1920 * 1080 * 3 / 2
	srcBuf := make([]byte, yuvSize)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Pipeline buffer
		pBuf := fp.Acquire()
		copy(pBuf[:yuvSize], srcBuf)

		// Relay buffer
		rBuf := fp.Acquire()
		copy(rBuf[:yuvSize], srcBuf)

		// Simulate: relay copies to encoderYUV, releases immediately
		fp.Release(rBuf)
		// Pipeline releases after processing
		fp.Release(pBuf)
	}
}
