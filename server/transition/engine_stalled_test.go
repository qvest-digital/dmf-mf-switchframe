package transition

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
)

func uniformYUV420(w, h int, y byte) []byte {
	buf := make([]byte, video.YUV420FrameSize(w, h))
	for i := range buf {
		buf[i] = 128
	}
	for i := 0; i < w*h; i++ {
		buf[i] = y
	}
	return buf
}

// A side is stalled only after it has delivered nothing for the threshold.
// One that delivered a frame is blended again with it; one that delivered
// nothing asks for a fill, and a fill does not count as delivering.
func TestEngine_ReblendStalled(t *testing.T) {
	const w, h = 16, 16
	var mu sync.Mutex
	var outs []byte
	e := NewEngine(EngineConfig{
		Output: func(yuv []byte, _, _ int, _ int64, _ bool) {
			mu.Lock()
			outs = append(outs, yuv[0])
			mu.Unlock()
		},
		HintWidth:  w,
		HintHeight: h,
	})
	require.NoError(t, e.Start("a", "b", Mix, 10_000))
	defer e.Abort()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(outs) }
	const after = 20 * time.Millisecond

	reblended, needFill := e.ReblendStalled("b", after, 1)
	require.False(t, reblended || needFill, "no side is stalled right after the start")

	time.Sleep(2 * after)
	_, needFill = e.ReblendStalled("a", after, 1)
	require.True(t, needFill, "a side that delivered nothing needs a fill")
	e.IngestFillFrame("a", uniformYUV420(w, h, 16), w, h, 1)
	_, needFill = e.ReblendStalled("a", after, 2)
	require.True(t, needFill, "a fill is not a delivered frame")

	e.IngestRawFrame("b", uniformYUV420(w, h, 50), w, h, 3)
	require.Equal(t, 1, count())
	reblended, needFill = e.ReblendStalled("b", after, 4)
	require.False(t, reblended || needFill, "b delivered within the threshold")

	time.Sleep(2 * after)
	reblended, needFill = e.ReblendStalled("b", after, 5)
	require.True(t, reblended)
	require.False(t, needFill)
	require.Equal(t, 2, count(), "b's held frame drew a second output")

	reblended, needFill = e.ReblendStalled("other", after, 6)
	require.False(t, reblended || needFill, "not a side of this transition")
}
