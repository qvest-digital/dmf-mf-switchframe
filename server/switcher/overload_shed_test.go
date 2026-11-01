package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
)

// onceSlowNode takes delay on its slowAt-th frame only.
type onceSlowNode struct {
	slowPipelineNode
	slowAt, frames int
}

func (n *onceSlowNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	n.frames++
	if n.frames == n.slowAt {
		time.Sleep(n.delay)
	}
	return src
}

// One tick that takes longer than a frame period does not shed the tick
// after it: that tick can still be shown within its own grain. Only a tick
// whose grain has already ended when the loop takes it skips its video.
func TestOneLongTickDoesNotShedTheNextOnTimeTick(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()
	s.RegisterSource("cam1", distribution.NewRelay())
	s.configMu.Lock()
	s.frameSync.AddSource("cam1")
	s.configMu.Unlock()
	require.NoError(t, s.Cut(context.Background(), "cam1"))
	s.lastBroadcastVFrame.Store(&media.VideoFrame{WireData: []byte{0}, IsKeyframe: true})

	p := &Pipeline{}
	require.NoError(t, p.Build(PipelineFormat{
		Name: "test", Width: 320, Height: 240, FPSNum: 30, FPSDen: 1,
	}, s.framePool.Load(), []PipelineNode{&onceSlowNode{slowPipelineNode: slowPipelineNode{delay: 45 * time.Millisecond}, slowAt: 2}}))
	s.pipeline.Store(p)

	pool := s.framePool.Load()
	for i := 0; i < 5; i++ {
		pf := &ProcessingFrame{Data: pool.Acquire()[:320*240*3/2], Width: 320, Height: 240, PTS: int64(i) * 3000, pool: pool}
		pf.SetRefs(1)
		s.frameSync.IngestRawVideo("cam1", pf)
	}
	for i := 0; i < 5; i++ {
		s.processTick(clock.Tick{
			Number:        int64(i),
			VideoPTS:      int64(i) * 3000,
			ReadPTS:       int64(i) * 3000,
			AudioSamples:  1600,
			FrameDuration: 33333 * time.Microsecond,
		})
	}

	require.Zero(t, s.overloadDrops.Load(),
		"a tick after one long tick was shed although it was on time")
}
