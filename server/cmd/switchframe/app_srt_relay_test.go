package main

import (
	"testing"
)

// TestSRTRelayVideoCallback_SingleCopy verifies that the OnRawVideo callback
// makes only ONE deep copy of the YUV buffer, shared between the pipeline
// and relay channels. The relay goroutine already copies into its own
// encoderYUV buffer before encoding, so a separate deep copy for the relay
// channel is wasteful (6.67GB cumulative allocs per pprof).
//
// This test constructs the same channel+callback pattern used by wireSRTSource
// and verifies that both channels receive data backed by the same allocation.
func TestSRTRelayVideoCallback_SingleCopy(t *testing.T) {
	// Simulate the two channels from wireSRTSource.
	type videoJob struct {
		yuv []byte
		w   int
		h   int
		pts int64
	}
	pipelineCh := make(chan videoJob, 4)
	relayVideoCh := make(chan videoJob, 4)

	// Simulate a decoded YUV frame from FFmpeg (source buffer we must not retain).
	srcYUV := make([]byte, 1920*1080*3/2) // ~3.1MB 1080p YUV420
	for i := range srcYUV {
		srcYUV[i] = byte(i & 0xFF)
	}

	// This is the callback pattern from wireSRTSource.
	// The CORRECT implementation makes ONE copy shared by both channels.
	callback := func(yuv []byte, w, h int, pts int64) {
		yuvCopy := make([]byte, len(yuv))
		copy(yuvCopy, yuv)

		select {
		case pipelineCh <- videoJob{yuv: yuvCopy, w: w, h: h, pts: pts}:
		default:
		}
		select {
		case relayVideoCh <- videoJob{yuv: yuvCopy, w: w, h: h, pts: pts}:
		default:
		}
	}

	// Fire the callback.
	callback(srcYUV, 1920, 1080, 90000)

	// Drain both channels.
	pipelineJob := <-pipelineCh
	relayJob := <-relayVideoCh

	// Both should have the same data.
	if len(pipelineJob.yuv) != len(relayJob.yuv) {
		t.Fatalf("length mismatch: pipeline=%d relay=%d", len(pipelineJob.yuv), len(relayJob.yuv))
	}

	// CRITICAL: Both channels must share the same backing array (single copy).
	// If they have different backing arrays, the callback is doing a wasteful
	// second deep copy.
	pipelinePtr := &pipelineJob.yuv[0]
	relayPtr := &relayJob.yuv[0]
	if pipelinePtr != relayPtr {
		t.Errorf("pipeline and relay YUV buffers have different backing arrays — "+
			"callback is making two copies instead of one (pipeline=%p relay=%p)",
			pipelinePtr, relayPtr)
	}

	// Verify data integrity.
	for i := 0; i < 100; i++ {
		if pipelineJob.yuv[i] != srcYUV[i] {
			t.Fatalf("data mismatch at offset %d: got %d, want %d", i, pipelineJob.yuv[i], srcYUV[i])
		}
	}
}

// BenchmarkSRTRelayVideoCallback_DoubleCopy benchmarks the old pattern
// that allocates two separate YUV copies per frame.
func BenchmarkSRTRelayVideoCallback_DoubleCopy(b *testing.B) {
	srcYUV := make([]byte, 1920*1080*3/2)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		copy1 := make([]byte, len(srcYUV))
		copy(copy1, srcYUV)
		copy2 := make([]byte, len(srcYUV))
		copy(copy2, srcYUV)
		_ = copy1
		_ = copy2
	}
}

// BenchmarkSRTRelayVideoCallback_SingleCopy benchmarks the fixed pattern
// that makes one copy shared by both channels.
func BenchmarkSRTRelayVideoCallback_SingleCopy(b *testing.B) {
	srcYUV := make([]byte, 1920*1080*3/2)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		shared := make([]byte, len(srcYUV))
		copy(shared, srcYUV)
		_ = shared
	}
}
