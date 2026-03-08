package codec

import (
	"encoding/binary"
	"testing"
)

// buildRealisticAVC1 constructs a realistic AVC1 frame containing:
//   - SPS NALU (~32 bytes): Sequence Parameter Set
//   - PPS NALU (~8 bytes): Picture Parameter Set
//   - IDR NALU (~50KB): Keyframe slice data
//
// This mimics a typical H.264 keyframe from a 720p/1080p encoder.
func buildRealisticAVC1() []byte {
	spsData := make([]byte, 32)
	spsData[0] = 0x67 // SPS NALU type
	for i := 1; i < len(spsData); i++ {
		spsData[i] = byte(i * 7 % 256)
	}

	ppsData := make([]byte, 8)
	ppsData[0] = 0x68 // PPS NALU type
	for i := 1; i < len(ppsData); i++ {
		ppsData[i] = byte(i * 13 % 256)
	}

	idrData := make([]byte, 50*1024)
	idrData[0] = 0x65 // IDR NALU type
	for i := 1; i < len(idrData); i++ {
		idrData[i] = byte(i % 256)
	}

	// AVC1 format: [4-byte length][NALU data] for each NALU
	totalSize := (4 + len(spsData)) + (4 + len(ppsData)) + (4 + len(idrData))
	avc1 := make([]byte, totalSize)
	pos := 0

	// SPS
	binary.BigEndian.PutUint32(avc1[pos:], uint32(len(spsData)))
	pos += 4
	copy(avc1[pos:], spsData)
	pos += len(spsData)

	// PPS
	binary.BigEndian.PutUint32(avc1[pos:], uint32(len(ppsData)))
	pos += 4
	copy(avc1[pos:], ppsData)
	pos += len(ppsData)

	// IDR
	binary.BigEndian.PutUint32(avc1[pos:], uint32(len(idrData)))
	pos += 4
	copy(avc1[pos:], idrData)

	return avc1
}

// buildRealisticAnnexB constructs the Annex B equivalent of buildRealisticAVC1.
// Uses 4-byte start codes (0x00000001) before each NALU.
func buildRealisticAnnexB() []byte {
	spsData := make([]byte, 32)
	spsData[0] = 0x67
	for i := 1; i < len(spsData); i++ {
		spsData[i] = byte(i * 7 % 256)
	}

	ppsData := make([]byte, 8)
	ppsData[0] = 0x68
	for i := 1; i < len(ppsData); i++ {
		ppsData[i] = byte(i * 13 % 256)
	}

	idrData := make([]byte, 50*1024)
	idrData[0] = 0x65
	for i := 1; i < len(idrData); i++ {
		idrData[i] = byte(i % 256)
	}

	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	totalSize := (4 + len(spsData)) + (4 + len(ppsData)) + (4 + len(idrData))
	annexB := make([]byte, totalSize)
	pos := 0

	copy(annexB[pos:], startCode)
	pos += 4
	copy(annexB[pos:], spsData)
	pos += len(spsData)

	copy(annexB[pos:], startCode)
	pos += 4
	copy(annexB[pos:], ppsData)
	pos += len(ppsData)

	copy(annexB[pos:], startCode)
	pos += 4
	copy(annexB[pos:], idrData)

	return annexB
}

// BenchmarkAVC1ToAnnexB benchmarks conversion from AVC1 (length-prefixed)
// to Annex B (start-code-prefixed) format. This runs on every frame in the
// output muxer path.
func BenchmarkAVC1ToAnnexB(b *testing.B) {
	avc1 := buildRealisticAVC1()

	b.SetBytes(int64(len(avc1)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		AVC1ToAnnexB(avc1)
	}
}

// BenchmarkAnnexBToAVC1 benchmarks the reverse conversion from Annex B to
// AVC1 format. This is used when ingesting raw H.264 streams.
func BenchmarkAnnexBToAVC1(b *testing.B) {
	annexB := buildRealisticAnnexB()

	b.SetBytes(int64(len(annexB)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		AnnexBToAVC1(annexB)
	}
}

// BenchmarkAnnexBToAVC1Into benchmarks the pooled variant that appends to a
// reusable destination buffer, avoiding per-frame output allocation.
func BenchmarkAnnexBToAVC1Into(b *testing.B) {
	annexB := buildRealisticAnnexB()

	buf := make([]byte, 0, len(annexB)+64)
	b.SetBytes(int64(len(annexB)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf = AnnexBToAVC1Into(annexB, buf[:0])
	}
}

// BenchmarkExtractNALUs benchmarks extracting individual NALUs from an AVC1
// frame. This is used when the switcher needs to inspect NALU types (e.g.,
// to detect keyframes by checking for IDR slices).
func BenchmarkExtractNALUs(b *testing.B) {
	avc1 := buildRealisticAVC1()

	b.SetBytes(int64(len(avc1)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ExtractNALUs(avc1)
	}
}

// BenchmarkAVC1ToAnnexB_SmallPFrame benchmarks AVC1-to-Annex-B conversion
// for a small P-frame (~2KB), representative of inter-coded frames that
// make up the bulk of a video stream.
func BenchmarkAVC1ToAnnexB_SmallPFrame(b *testing.B) {
	pFrameData := make([]byte, 2048)
	pFrameData[0] = 0x41 // non-IDR slice NALU type
	for i := 1; i < len(pFrameData); i++ {
		pFrameData[i] = byte(i % 256)
	}

	avc1 := make([]byte, 4+len(pFrameData))
	binary.BigEndian.PutUint32(avc1, uint32(len(pFrameData)))
	copy(avc1[4:], pFrameData)

	b.SetBytes(int64(len(avc1)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		AVC1ToAnnexB(avc1)
	}
}
