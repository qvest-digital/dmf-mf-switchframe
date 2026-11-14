package mxl

import (
	"fmt"
	"testing"
)

// The four frame converters at the widths of HD and UHD, whose rows are
// whole 6-pixel groups, and of 720p, whose rows end in a partial group.
func BenchmarkV210Frame(b *testing.B) {
	for _, size := range []struct{ w, h int }{{1920, 1080}, {3840, 2160}, {1280, 720}} {
		w, h := size.w, size.h
		yuv420 := make([]byte, w*h*3/2)
		yuv422p10 := make([]byte, w*h*4)
		v210 := make([]byte, V210BufSize(w, h))
		cbTmp, crTmp := make([]byte, w), make([]byte, w)
		for i := range yuv420 {
			yuv420[i] = byte(i * 7)
		}
		for i := 0; i < len(yuv422p10); i += 2 {
			yuv422p10[i] = byte(i)
			yuv422p10[i+1] = byte(i>>8) & 3
		}
		run := func(name string, fn func() error) {
			b.Run(fmt.Sprintf("%s/%dx%d", name, w, h), func(b *testing.B) {
				if err := fn(); err != nil {
					b.Skip(err)
				}
				b.SetBytes(int64(len(v210)))
				b.ResetTimer()
				for b.Loop() {
					_ = fn()
				}
			})
		}
		run("pack8", func() error { return YUV420pToV210Into(yuv420, v210, w, h) })
		run("unpack8", func() error { return V210ToYUV420pInto(v210, yuv420, cbTmp, crTmp, w, h) })
		run("pack10", func() error { return YUV422P10ToV210Into(yuv422p10, v210, w, h) })
		run("unpack10", func() error { return V210ToYUV422P10Into(v210, yuv422p10, V210LineStride(w), w, h) })
	}
}
