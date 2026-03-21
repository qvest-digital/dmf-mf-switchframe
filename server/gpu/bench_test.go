//go:build cgo && cuda

package gpu

import (
	"sync/atomic"
	"testing"
)

// GPU benchmarks measure actual kernel execution time on the NVIDIA L4.
// Run with: go test -tags cuda -bench=. -benchtime=100x

func setupBenchCtx(b *testing.B) (*Context, *FramePool) {
	b.Helper()
	ctx, err := NewContext()
	if err != nil {
		b.Fatal(err)
	}
	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	if err != nil {
		ctx.Close()
		b.Fatal(err)
	}
	return ctx, pool
}

func BenchmarkUpload1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	yuv := make([]byte, 1920*1080*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Upload(ctx, frame, yuv, 1920, 1080)
	}
}

func BenchmarkDownload1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()
	FillBlack(ctx, frame)

	yuv := make([]byte, 1920*1080*3/2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Download(ctx, yuv, frame, 1920, 1080)
	}
}

func BenchmarkScaleBilinear1080to540(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	src, _ := pool.Acquire()
	defer src.Release()
	FillBlack(ctx, src)

	dstPool, _ := NewFramePool(ctx, 960, 540, 2)
	defer dstPool.Close()
	dst, _ := dstPool.Acquire()
	defer dst.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleBilinear(ctx, dst, src)
	}
}

func BenchmarkBlendMix1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	a, _ := pool.Acquire()
	bb, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer a.Release()
	defer bb.Release()
	defer dst.Release()
	FillBlack(ctx, a)
	FillBlack(ctx, bb)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendMix(ctx, dst, a, bb, 0.5)
	}
}

func BenchmarkBlendFTB1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()
	FillBlack(ctx, src)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendFTB(ctx, dst, src, 0.5)
	}
}

func BenchmarkChromaKey1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	mask, _ := pool.Acquire()
	defer frame.Release()
	defer mask.Release()
	FillBlack(ctx, frame)

	cfg := ChromaKeyConfig{
		KeyCb: 44, KeyCr: 21,
		Similarity: 0.3, Smoothness: 0.1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ChromaKey(ctx, frame, mask, cfg)
	}
}

func BenchmarkLumaKey1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	mask, _ := pool.Acquire()
	defer frame.Release()
	defer mask.Release()
	FillBlack(ctx, frame)

	lut := BuildLumaKeyLUT(50, 150, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LumaKey(ctx, frame, mask, lut)
	}
}

func BenchmarkPIPComposite1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	dst, _ := pool.Acquire()
	defer dst.Release()
	FillBlack(ctx, dst)

	srcPool, _ := NewFramePool(ctx, 640, 480, 2)
	defer srcPool.Close()
	src, _ := srcPool.Acquire()
	defer src.Release()
	FillBlack(ctx, src)

	rect := Rect{X: 960, Y: 540, W: 640, H: 480}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PIPComposite(ctx, dst, src, rect, 1.0)
	}
}

func BenchmarkDSKComposite1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()
	FillBlack(ctx, frame)

	rgba := make([]byte, 1920*1080*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i+3] = 128 // 50% alpha
	}
	overlay, _ := UploadOverlay(ctx, rgba, 1920, 1080)
	defer FreeOverlay(overlay)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DSKCompositeFullFrame(ctx, frame, overlay, 1.0)
	}
}

func setupSTMapBench(b *testing.B) (*Context, *FramePool, *GPUFrame, *GPUFrame, *GPUSTMap) {
	b.Helper()
	ctx, pool := setupBenchCtx(b)

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	FillBlack(ctx, src)

	w, h := 1920, 1080
	s := make([]float32, w*h)
	t := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(x) / float32(w-1)
			t[y*w+x] = float32(y) / float32(h-1)
		}
	}
	stmap, _ := UploadSTMap(ctx, s, t, w, h)
	return ctx, pool, src, dst, stmap
}

// BenchmarkSTMapWarp1080p uses the texture-based path (hardware bilinear for Y plane)
func BenchmarkSTMapWarp1080p(b *testing.B) {
	ctx, pool, src, dst, stmap := setupSTMapBench(b)
	defer ctx.Close()
	defer pool.Close()
	defer src.Release()
	defer dst.Release()
	defer stmap.Free()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		STMapWarp(ctx, dst, src, stmap)
	}
}

// BenchmarkSTMapWarpGlobalMem1080p uses the global memory path (manual bilinear)
func BenchmarkSTMapWarpGlobalMem1080p(b *testing.B) {
	ctx, pool, src, dst, stmap := setupSTMapBench(b)
	defer ctx.Close()
	defer pool.Close()
	defer src.Release()
	defer dst.Release()
	defer stmap.Free()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		STMapWarpGlobalMem(ctx, dst, src, stmap)
	}
}

func BenchmarkFRUCInterpolate1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	prev, _ := pool.Acquire()
	curr, _ := pool.Acquire()
	out, _ := pool.Acquire()
	defer prev.Release()
	defer curr.Release()
	defer out.Release()
	FillBlack(ctx, prev)
	FillBlack(ctx, curr)

	fruc, err := NewFRUC(ctx, 1920, 1080)
	if err != nil {
		b.Fatal(err)
	}
	defer fruc.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fruc.Interpolate(prev, curr, out, 0.5)
	}
}

func BenchmarkV210ToNV121080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	w, h := 1920, 1080
	v210Stride := V210LineStride(w)
	v210 := make([]byte, v210Stride*h)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UploadV210(ctx, frame, v210, w, h)
	}
}

func BenchmarkGPUEncode1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()
	FillBlack(ctx, frame)

	enc, err := NewGPUEncoder(ctx, 1920, 1080, 30, 1, 10_000_000)
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.PTS = int64(i * 3000)
		enc.EncodeGPU(frame, i == 0)
	}
}

func BenchmarkFullPipeline1080p(b *testing.B) {
	ctx, pool := setupBenchCtx(b)
	defer ctx.Close()
	defer pool.Close()

	enc, err := NewGPUEncoder(ctx, 1920, 1080, 30, 1, 10_000_000)
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()

	pipe := NewGPUPipeline(ctx, pool)
	forceIDR := &atomic.Bool{}
	pipe.Build(1920, 1080, pool.Pitch(), []GPUPipelineNode{
		NewGPUPassthroughNode("gpu_key", false),
		NewGPUPassthroughNode("gpu_layout", false),
		NewGPUPassthroughNode("gpu_dsk", false),
		NewGPUPassthroughNode("gpu_stmap", false),
		NewGPUEncodeNode(ctx, enc, forceIDR, func([]byte, bool, int64) {}),
	})
	defer pipe.Close()

	yuv := make([]byte, 1920*1080*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, _ := pipe.RunWithUpload(yuv, 1920, 1080, int64(i*3000))
		if frame != nil {
			frame.Release()
		}
	}
}
