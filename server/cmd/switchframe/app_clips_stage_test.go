package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/switcher"
)

// A cued clip's staged picture is a frame pool buffer, at the clip's size
// or scaled to the pipeline's. Dropped, the buffer goes back to the pool;
// put, the switcher owns it and releases it once, here at once, as no
// source takes it. A staged buffer that went neither way would be lost
// to the pool for good, 3 MB at 1080p each time.
func TestStagedClipFrameGoesBackToThePool(t *testing.T) {
	sw := switcher.New(distribution.NewRelay())
	t.Cleanup(sw.Close)
	pf := sw.PipelineFormat()
	pool := sw.GetFramePool()
	stage := stageClipFrame(sw)

	// top puts a buffer on top of the pool's free list and returns it:
	// the next Acquire takes it, and takes it again after it went back.
	top := func() *byte {
		b := pool.Acquire()
		pool.Release(b)
		return &b[0]
	}
	sizes := map[string][2]int{
		"pipeline size": {pf.Width, pf.Height},
		"scaled":        {pf.Width / 2, pf.Height / 2},
	}
	for name, wh := range sizes {
		yuv := make([]byte, wh[0]*wh[1]*3/2)

		b := top()
		_, drop := stage("clip:1", yuv, wh[0], wh[1], true)
		drop()
		require.Same(t, b, top(), "%s: a dropped stage goes back to the pool", name)

		b = top()
		put, _ := stage("clip:1", yuv, wh[0], wh[1], true)
		put(1000)
		require.Same(t, b, top(), "%s: a put stage is released once the switcher drops it", name)
	}
}
