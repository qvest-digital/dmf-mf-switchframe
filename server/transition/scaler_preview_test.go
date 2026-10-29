package transition

import (
	"bytes"
	"math"
	"runtime"
	"testing"

	"github.com/zsiec/switchframe/server/internal/video"
)

// stripes420 is a w x h 4:2:0 frame of vertical luma stripes, period
// pixels wide, on neutral chroma.
func stripes420(w, h, period int) []byte {
	f := make([]byte, video.YUV420FrameSize(w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x/(period/2))%2 == 0 {
				f[y*w+x] = 235
			} else {
				f[y*w+x] = 16
			}
		}
	}
	for i := w * h; i < len(f); i++ {
		f[i] = 128
	}
	return f
}

func lumaStddev(f []byte, w, h int) float64 {
	n := float64(w * h)
	var sum, sq float64
	for _, v := range f[:w*h] {
		sum += float64(v)
		sq += float64(v) * float64(v)
	}
	mean := sum / n
	return math.Sqrt(sq/n - mean*mean)
}

// A target more than half the source, such as 1080p to the 720p program
// monitor, keeps the detail a direct scale keeps. A 2x box pre-shrink
// there went down to 960x540 and scaled back up, losing about 40% of
// the contrast of fine detail.
func TestScaleYUV420Preview_KeepsDetailAboveHalfSize(t *testing.T) {
	for _, c := range []struct{ srcW, srcH, dstW, dstH int }{
		{1920, 1080, 1280, 720},
		{1280, 720, 852, 480},
	} {
		src := stripes420(c.srcW, c.srcH, 6)
		size := video.YUV420FrameSize(c.dstW, c.dstH)
		direct := make([]byte, size)
		ScaleYUV420(src, c.srcW, c.srcH, direct, c.dstW, c.dstH)
		got := make([]byte, size)
		var box []byte
		ScaleYUV420Preview(src, c.srcW, c.srcH, got, c.dstW, c.dstH, &box)

		want, have := lumaStddev(direct, c.dstW, c.dstH), lumaStddev(got, c.dstW, c.dstH)
		if have < 0.95*want {
			t.Errorf("%dx%d -> %dx%d: luma stddev %.1f, direct scale %.1f", c.srcW, c.srcH, c.dstW, c.dstH, have, want)
		}
	}
}

// From at least twice the target, as 1080p to the 480p tiles, the box
// pre-shrink still halves the source first: the half size covers the
// target, so it loses nothing the target could show, and detail finer
// than the target averages out instead of aliasing.
func TestScaleYUV420Preview_PreShrinksFromTwiceTheTarget(t *testing.T) {
	const srcW, srcH, dstW, dstH = 1920, 1080, 852, 480
	src := stripes420(srcW, srcH, 6)
	got := make([]byte, video.YUV420FrameSize(dstW, dstH))
	var box []byte
	ScaleYUV420Preview(src, srcW, srcH, got, dstW, dstH, &box)

	if runtime.GOARCH != "amd64" {
		// Only the amd64 build has the box path; the others scale directly.
		want := make([]byte, len(got))
		ScaleYUV420(src, srcW, srcH, want, dstW, dstH)
		if !bytes.Equal(got, want) {
			t.Fatal("preview scale differs from the direct scale")
		}
		return
	}
	if len(box) != video.YUV420FrameSize(srcW/2, srcH/2) {
		t.Fatalf("1080p -> 480p no longer goes through the box pre-shrink: box buffer %d bytes", len(box))
	}
	half := make([]byte, video.YUV420FrameSize(srcW/2, srcH/2))
	BoxShrink2xYUV420(src, srcW, srcH, half, srcW/2, srcH/2)
	want := make([]byte, len(got))
	ScaleYUV420(half, srcW/2, srcH/2, want, dstW, dstH)
	if !bytes.Equal(got, want) {
		t.Fatal("1080p -> 480p no longer goes through the box pre-shrink")
	}
}
