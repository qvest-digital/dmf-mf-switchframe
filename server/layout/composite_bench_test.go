package layout

import (
	"image"
	"testing"
)

// BenchmarkBlendRegion_QuarterPIP benchmarks a 480x270 PIP overlay onto 1920x1080
// (typical quarter-screen corner PIP).
func BenchmarkBlendRegion_QuarterPIP(b *testing.B) {
	dstW, dstH := 1920, 1080
	srcW, srcH := 480, 270
	dst := makeYUV420(dstW, dstH, 16, 128, 128)
	src := makeYUV420(srcW, srcH, 200, 100, 150)
	rect := image.Rect(1440, 810, 1920, 1080) // bottom-right corner

	b.SetBytes(int64(srcW*srcH + srcW*srcH/4 + srcW*srcH/4)) // Y + Cb + Cr
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendRegion(dst, dstW, dstH, src, srcW, srcH, rect, 0.75)
	}
}

// BenchmarkBlendRegion_SideBySide benchmarks a 960x540 half-screen overlay onto 1920x1080.
func BenchmarkBlendRegion_SideBySide(b *testing.B) {
	dstW, dstH := 1920, 1080
	srcW, srcH := 960, 540
	dst := makeYUV420(dstW, dstH, 16, 128, 128)
	src := makeYUV420(srcW, srcH, 200, 100, 150)
	rect := image.Rect(960, 0, 1920, 540)

	b.SetBytes(int64(srcW*srcH + srcW*srcH/4 + srcW*srcH/4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendRegion(dst, dstW, dstH, src, srcW, srcH, rect, 0.5)
	}
}

// BenchmarkComposePIPOpaque_QuarterPIP benchmarks opaque compositing (no blend math).
func BenchmarkComposePIPOpaque_QuarterPIP(b *testing.B) {
	dstW, dstH := 1920, 1080
	srcW, srcH := 480, 270
	dst := makeYUV420(dstW, dstH, 16, 128, 128)
	src := makeYUV420(srcW, srcH, 200, 100, 150)
	rect := image.Rect(1440, 810, 1920, 1080)

	b.SetBytes(int64(srcW*srcH + srcW*srcH/4 + srcW*srcH/4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComposePIPOpaque(dst, dstW, dstH, src, srcW, srcH, rect)
	}
}

// BenchmarkDrawBorderYUV benchmarks border drawing around a quarter-screen PIP.
func BenchmarkDrawBorderYUV_QuarterPIP(b *testing.B) {
	dstW, dstH := 1920, 1080
	dst := makeYUV420(dstW, dstH, 16, 128, 128)
	rect := image.Rect(1440, 810, 1920, 1080)
	color := [3]byte{235, 128, 128}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DrawBorderYUV(dst, dstW, dstH, rect, color, 4)
	}
}
