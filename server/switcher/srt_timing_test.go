package switcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// srcPTSGPUMock records the source frame PTS each RunFromCache asks for.
type srcPTSGPUMock struct {
	syncLoopGPUMock
	mu     sync.Mutex
	srcPTS []int64
}

func (m *srcPTSGPUMock) RunFromCache(sourceKey string, srcPTS, pts int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.srcPTS = append(m.srcPTS, srcPTS)
	return nil
}

func (m *srcPTSGPUMock) asked() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.srcPTS...)
}

// An SRT source decoded on the GPU caches each frame in VRAM under the PTS
// its producer placed on the engine timeline, and hands the switcher a
// frame without pixels carrying that PTS. The tick that shows the frame
// must look the cached frame up under that same PTS; a PTS restamped on
// arrival matches no cached frame, the frame without pixels is uploaded
// instead, and the program stops.
func TestSRTFrameKeepsItsProducerPTSForTheGPUCache(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()

	s.RegisterSRTSource("srt:a")
	require.NoError(t, s.Cut(context.Background(), "srt:a"))
	gpu := &srcPTSGPUMock{}
	gpu.onRunWithUpload = func(yuv []byte, w, h int, pts int64) error {
		if len(yuv) == 0 {
			return fmt.Errorf("YUV buffer too small: 0")
		}
		return nil
	}
	s.SetGPUPipeline(gpu)

	const pts = int64(900_000)
	s.IngestRawVideo("srt:a", &ProcessingFrame{Width: 320, Height: 240, PTS: pts, DTS: pts})
	s.processVideoTick(clock.Tick{
		VideoPTS:      pts + 6000,
		ReadPTS:       pts,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	})

	require.Equal(t, []int64{pts}, gpu.asked(),
		"the tick must look the GPU-cached SRT frame up under the PTS it was cached with")
}
