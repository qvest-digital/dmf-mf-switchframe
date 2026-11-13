package transition

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// The clip player and the playout channel scale each picture of a clip to
// the pipeline on the goroutine that paces their output, which has a frame
// period, 20 ms at 50 fps, for decode, scale and preview encode; the
// upscale of a 720p clip to 1080p alone took about half of that on one
// core. A large plane is therefore cut into row bands: the caller scales
// only the first, ceil(dstH/workers) rows, and every row is scaled exactly
// once.
func TestScaleBandsLeaveTheCallerOneBand(t *testing.T) {
	for _, tc := range []struct{ dstH, workers int }{
		{1080, 4}, {1080, 2}, {1025, 4}, {1023, 3}, {513, 4}, {7, 4}, {3, 4}, {2, 4}, {1, 4}, {5, 2},
	} {
		var mu sync.Mutex
		done := make([]int, tc.dstH)
		var caller [][2]int
		var spawned int
		callerG := goroutineID()
		scaleBands(tc.dstH, tc.workers, func(r0, r1 int) {
			mu.Lock()
			defer mu.Unlock()
			for r := r0; r < r1; r++ {
				done[r]++
			}
			if goroutineID() == callerG {
				caller = append(caller, [2]int{r0, r1})
			} else {
				spawned++
			}
		})
		band := (tc.dstH + tc.workers - 1) / tc.workers
		require.Equal(t, [][2]int{{0, min(band, tc.dstH)}}, caller,
			"dstH %d workers %d: the caller's rows", tc.dstH, tc.workers)
		require.LessOrEqual(t, spawned, tc.workers-1, "dstH %d workers %d: bands off the caller", tc.dstH, tc.workers)
		for r, n := range done {
			require.Equal(t, 1, n, "dstH %d workers %d: row %d scaled %d times", tc.dstH, tc.workers, r, n)
		}
	}
}

// goroutineID returns the calling goroutine's id from its stack header.
func goroutineID() string {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	// "goroutine 123 [running]:..."
	for i := len("goroutine "); i < len(buf); i++ {
		if buf[i] == ' ' {
			return string(buf[len("goroutine "):i])
		}
	}
	return string(buf)
}

// Banding changes which goroutine scales a row, never its bytes: odd
// sizes, up and down, and a plane under the banding threshold.
func TestScaleYUV420BandedOutputIsByteIdentical(t *testing.T) {
	for _, tc := range []struct{ srcW, srcH, dstW, dstH int }{
		{1001, 999, 1023, 1025},
		{513, 3, 1025, 513},
		{3840, 2160, 1920, 1080},
		{1280, 720, 1920, 1080},
		{640, 360, 320, 180},
	} {
		src := make([]byte, video.YUV420FrameSize(tc.srcW, tc.srcH))
		for i := range src {
			src[i] = byte(i*7 + i/tc.srcW)
		}
		got := make([]byte, video.YUV420FrameSize(tc.dstW, tc.dstH))
		want := make([]byte, len(got))
		ScaleYUV420(src, tc.srcW, tc.srcH, got, tc.dstW, tc.dstH)
		scaleYUV420OneRowAtATime(src, tc.srcW, tc.srcH, want, tc.dstW, tc.dstH)
		require.Equal(t, want, got, "%dx%d -> %dx%d", tc.srcW, tc.srcH, tc.dstW, tc.dstH)
	}

	srcW, srcH, dstW, dstH := 1280, 720, 1920, 1080
	src := make([]byte, video.YUV420FrameSize(srcW, srcH))
	dst := make([]byte, video.YUV420FrameSize(dstW, dstH))
	fastest := func(f func()) time.Duration {
		best := time.Duration(1 << 62)
		for range 5 {
			start := time.Now()
			f()
			best = min(best, time.Since(start))
		}
		return best
	}
	banded := fastest(func() { ScaleYUV420(src, srcW, srcH, dst, dstW, dstH) })
	oneRow := fastest(func() { scaleYUV420OneRowAtATime(src, srcW, srcH, dst, dstW, dstH) })
	t.Logf("720p -> 1080p: %v banded (%d workers), %v one row at a time", banded, scaleWorkers(), oneRow)
}

// scaleYUV420OneRowAtATime is ScaleYUV420 on the calling goroutine alone.
func scaleYUV420OneRowAtATime(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	plane := func(s []byte, sw, sh int, d []byte, dw, dh int) {
		if sw == dw && sh == dh {
			copy(d, s)
			return
		}
		xs := make([]int64, dw)
		scalePlaneWithCoords(s, sw, sh, d, dw, dh, xs)
	}
	sy, dy := srcW*srcH, dstW*dstH
	suv, duv := (srcW/2)*(srcH/2), (dstW/2)*(dstH/2)
	plane(src[:sy], srcW, srcH, dst[:dy], dstW, dstH)
	plane(src[sy:sy+suv], srcW/2, srcH/2, dst[dy:dy+duv], dstW/2, dstH/2)
	plane(src[sy+suv:sy+2*suv], srcW/2, srcH/2, dst[dy+duv:dy+2*duv], dstW/2, dstH/2)
}
