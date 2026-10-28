package colorgrade

import (
	"os"
	"testing"
)

func benchFrame420(w, h int) []byte {
	// Smooth gradients with mild noise, closer to camera content than
	// random bytes, whose LUT accesses never hit the cache.
	f := make([]byte, w*h*3/2)
	n := uint32(1)
	noise := func() int { n = n*1664525 + 1013904223; return int(n>>29) - 4 }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			f[y*w+x] = byte(16 + (x*219)/w + noise())
		}
	}
	cw, ch := w/2, h/2
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			f[w*h+y*cw+x] = byte(16 + (y*224)/ch + noise())
			f[w*h+cw*ch+y*cw+x] = byte(240 - (x*224)/cw + noise())
		}
	}
	return f
}

func BenchmarkApplyYUV420_1080p(b *testing.B) {
	look := GenerateLook(BuiltinLooks()[0])
	g, err := NewGrader(look.Data, look.Size)
	if err != nil {
		b.Fatal(err)
	}
	src := benchFrame420(1920, 1080)
	f := make([]byte, len(src))
	for _, wk := range []int{1, 4, 8} {
		b.Run(map[int]string{1: "1worker", 4: "4workers", 8: "8workers"}[wk], func(b *testing.B) {
			old := gradeWorkers
			gradeWorkers = wk
			defer func() { gradeWorkers = old }()
			for i := 0; i < b.N; i++ {
				copy(f, src)
				_ = g.ApplyYUV420(f, 1920, 1080, 1)
			}
		})
	}
}

func BenchmarkApplyYUV422P10_1080p(b *testing.B) {
	look := GenerateLook(BuiltinLooks()[0])
	g, err := NewGrader(look.Data, look.Size)
	if err != nil {
		b.Fatal(err)
	}
	src := benchFrame420(1920, 1080)
	f := make([]byte, 1920*1080*4)
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			v := uint16(src[y*1920+x]) << 2
			f[(y*1920+x)*2], f[(y*1920+x)*2+1] = byte(v), byte(v>>8)
		}
	}
	for i := 1920 * 1080 * 2; i < len(f); i += 2 {
		f[i], f[i+1] = 0x00, 0x02
	}
	for _, wk := range []int{1, 4, 8} {
		b.Run(map[int]string{1: "1worker", 4: "4workers", 8: "8workers"}[wk], func(b *testing.B) {
			old := gradeWorkers
			gradeWorkers = wk
			defer func() { gradeWorkers = old }()
			for i := 0; i < b.N; i++ {
				_ = g.ApplyYUV422P10(f, 1920, 1080, 1)
			}
		})
	}
}

// BenchmarkApplyYUV420_1080pFirstFrame grades with an empty cache each time:
// the cost of the first frame after a look or corrector change.
func BenchmarkApplyYUV420_1080pFirstFrame(b *testing.B) {
	look := GenerateLook(BuiltinLooks()[0])
	src := benchFrame420(1920, 1080)
	f := make([]byte, len(src))
	for i := 0; i < b.N; i++ {
		copy(f, src)
		g, _ := NewGrader(look.Data, look.Size)
		_ = g.ApplyYUV420(f, 1920, 1080, 1)
	}
}

// BenchmarkApplyYUV420_RealFrames grades decoded camera frames named by
// SWITCHFRAME_BENCH_I420 (raw 1920x1080 yuv420p), cycling through them.
func BenchmarkApplyYUV420_RealFrames(b *testing.B) {
	path := os.Getenv("SWITCHFRAME_BENCH_I420")
	if path == "" {
		b.Skip("SWITCHFRAME_BENCH_I420 not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	const fs = 1920 * 1080 * 3 / 2
	n := len(raw) / fs
	look := GenerateLook(BuiltinLooks()[0])
	g, _ := NewGrader(look.Data, look.Size)
	f := make([]byte, fs)
	for i := 0; i < b.N; i++ {
		copy(f, raw[(i%n)*fs:])
		_ = g.ApplyYUV420(f, 1920, 1080, 1)
	}
}
