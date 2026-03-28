package mxl

import (
	"encoding/binary"
	"math/rand"
	"testing"
	"unsafe"
)

// Comprehensive 10-bit SIMD benchmark suite for the mxl package.
// Benchmarks Go reference vs SIMD dispatch for V210<->YUV422P10 conversion at 1080p.

// v210UnpackRow10GoRef is a pure Go reference implementation for benchmarking.
// Extracts Y, Cb, Cr from V210 packed data for groups of 6 pixels.
func v210UnpackRow10GoRef(yOut, cbOut, crOut []uint16, v210In []byte, groups int) {
	v210S := v210In
	for g := 0; g < groups; g++ {
		wordOff := g * 16

		w0 := binary.LittleEndian.Uint32(v210S[wordOff:])
		w1 := binary.LittleEndian.Uint32(v210S[wordOff+4:])
		w2 := binary.LittleEndian.Uint32(v210S[wordOff+8:])
		w3 := binary.LittleEndian.Uint32(v210S[wordOff+12:])

		yBase := g * 6
		yOut[yBase+0] = uint16((w0 >> 10) & 0x3FF)
		yOut[yBase+1] = uint16(w1 & 0x3FF)
		yOut[yBase+2] = uint16((w1 >> 20) & 0x3FF)
		yOut[yBase+3] = uint16((w2 >> 10) & 0x3FF)
		yOut[yBase+4] = uint16(w3 & 0x3FF)
		yOut[yBase+5] = uint16((w3 >> 20) & 0x3FF)

		cBase := g * 3
		cbOut[cBase+0] = uint16(w0 & 0x3FF)
		cbOut[cBase+1] = uint16((w1 >> 10) & 0x3FF)
		cbOut[cBase+2] = uint16((w2 >> 20) & 0x3FF)

		crOut[cBase+0] = uint16((w0 >> 20) & 0x3FF)
		crOut[cBase+1] = uint16(w2 & 0x3FF)
		crOut[cBase+2] = uint16((w3 >> 10) & 0x3FF)
	}
}

// v210PackRow10GoRef is a pure Go reference implementation for benchmarking.
// Packs Y, Cb, Cr uint16 samples into V210 format for groups of 6 pixels.
func v210PackRow10GoRef(v210Out []byte, yIn, cbIn, crIn []uint16, groups int) {
	v210S := unsafe.Slice(&v210Out[0], groups*16)
	for g := 0; g < groups; g++ {
		yBase := g * 6
		cBase := g * 3

		y0 := uint32(yIn[yBase+0])
		y1 := uint32(yIn[yBase+1])
		y2 := uint32(yIn[yBase+2])
		y3 := uint32(yIn[yBase+3])
		y4 := uint32(yIn[yBase+4])
		y5 := uint32(yIn[yBase+5])

		cb0 := uint32(cbIn[cBase+0])
		cb2 := uint32(cbIn[cBase+1])
		cb4 := uint32(cbIn[cBase+2])

		cr0 := uint32(crIn[cBase+0])
		cr2 := uint32(crIn[cBase+1])
		cr4 := uint32(crIn[cBase+2])

		wordOff := g * 16
		binary.LittleEndian.PutUint32(v210S[wordOff:],
			(cb0&0x3FF)|((y0&0x3FF)<<10)|((cr0&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+4:],
			(y1&0x3FF)|((cb2&0x3FF)<<10)|((y2&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+8:],
			(cr2&0x3FF)|((y3&0x3FF)<<10)|((cb4&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+12:],
			(y4&0x3FF)|((cr4&0x3FF)<<10)|((y5&0x3FF)<<20))
	}
}

// ============================================================================
// V210 -> YUV422P10LE (unpack/decode) — full-frame
// ============================================================================

func BenchmarkV210ToYUV422P10_Go_1080pFull(b *testing.B) {
	w, h := 1920, 1080
	stride := V210LineStride(w)
	v210 := make([]byte, stride*h)
	rng := rand.New(rand.NewSource(42))
	for i := range v210 {
		v210[i] = byte(rng.Intn(256))
	}

	b.SetBytes(int64(stride * h))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = V210ToYUV422P10(v210, stride, w, h)
	}
}

func BenchmarkV210ToYUV422P10_ASM_1080pFull(b *testing.B) {
	w, h := 1920, 1080
	stride := V210LineStride(w)
	v210 := make([]byte, stride*h)
	rng := rand.New(rand.NewSource(42))
	for i := range v210 {
		v210[i] = byte(rng.Intn(256))
	}

	b.SetBytes(int64(stride * h))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = v210ToYUV422P10SIMD(v210, stride, w, h)
	}
}

// Row-level unpack kernel

func BenchmarkV210UnpackRow10_Go_1080p(b *testing.B) {
	groups := 1920 / 6 // 320 groups for 1920 width
	v210 := make([]byte, groups*16)
	yOut := make([]uint16, groups*6)
	cbOut := make([]uint16, groups*3)
	crOut := make([]uint16, groups*3)
	rng := rand.New(rand.NewSource(42))
	for i := range v210 {
		v210[i] = byte(rng.Intn(256))
	}

	b.SetBytes(int64(groups * 16))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210UnpackRow10GoRef(yOut, cbOut, crOut, v210, groups)
	}
}

func BenchmarkV210UnpackRow10_ASM_1080p(b *testing.B) {
	groups := 1920 / 6
	v210 := make([]byte, groups*16)
	yOut := make([]uint16, groups*6)
	cbOut := make([]uint16, groups*3)
	crOut := make([]uint16, groups*3)
	rng := rand.New(rand.NewSource(42))
	for i := range v210 {
		v210[i] = byte(rng.Intn(256))
	}

	b.SetBytes(int64(groups * 16))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210UnpackRow10(yOut, cbOut, crOut, v210, groups)
	}
}

// ============================================================================
// YUV422P10LE -> V210 (pack/encode) — full-frame
// ============================================================================

func BenchmarkYUV422P10ToV210_Go_1080pFull(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	b.SetBytes(int64(len(yuv)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = YUV422P10ToV210(yuv, w, h)
	}
}

func BenchmarkYUV422P10ToV210_ASM_1080pFull(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	b.SetBytes(int64(len(yuv)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = yuv422P10ToV210SIMD(yuv, w, h)
	}
}

// Row-level pack kernel

func BenchmarkV210PackRow10_Go_1080p(b *testing.B) {
	groups := 1920 / 6
	v210Out := make([]byte, groups*16)
	yIn := make([]uint16, groups*6)
	cbIn := make([]uint16, groups*3)
	crIn := make([]uint16, groups*3)
	rng := rand.New(rand.NewSource(42))
	for i := range yIn {
		yIn[i] = uint16(rng.Intn(1024))
	}
	for i := range cbIn {
		cbIn[i] = uint16(rng.Intn(1024))
	}
	for i := range crIn {
		crIn[i] = uint16(rng.Intn(1024))
	}

	b.SetBytes(int64(groups * 16))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210PackRow10GoRef(v210Out, yIn, cbIn, crIn, groups)
	}
}

func BenchmarkV210PackRow10_ASM_1080p(b *testing.B) {
	groups := 1920 / 6
	v210Out := make([]byte, groups*16)
	yIn := make([]uint16, groups*6)
	cbIn := make([]uint16, groups*3)
	crIn := make([]uint16, groups*3)
	rng := rand.New(rand.NewSource(42))
	for i := range yIn {
		yIn[i] = uint16(rng.Intn(1024))
	}
	for i := range cbIn {
		cbIn[i] = uint16(rng.Intn(1024))
	}
	for i := range crIn {
		crIn[i] = uint16(rng.Intn(1024))
	}

	b.SetBytes(int64(groups * 16))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210PackRow10(v210Out, yIn, cbIn, crIn, groups)
	}
}
