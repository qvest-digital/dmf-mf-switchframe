package audio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestAudioDelayBuffer_ZeroDelay(t *testing.T) {
	buf := NewAudioDelayBuffer(0)
	frame := &media.AudioFrame{Data: []byte{1, 2, 3}, PTS: 100}
	out := buf.Ingest(frame)
	require.NotNil(t, out)
	assert.Equal(t, frame, out)
}

func TestAudioDelayBuffer_DelayedOutput(t *testing.T) {
	buf := NewAudioDelayBuffer(50)
	now := time.Now()

	f1 := &media.AudioFrame{Data: []byte{1}, PTS: 100}
	out := buf.ingestAt(f1, now)
	assert.Nil(t, out, "should not output before delay elapsed")

	f2 := &media.AudioFrame{Data: []byte{2}, PTS: 200}
	out = buf.ingestAt(f2, now.Add(50*time.Millisecond))
	require.NotNil(t, out)
	assert.Equal(t, f1.Data, out.Data, "should output first frame after delay")
}

func TestAudioDelayBuffer_SetDelay(t *testing.T) {
	buf := NewAudioDelayBuffer(0)
	buf.SetDelayMs(100)
	assert.Equal(t, 100, buf.DelayMs())
}

func TestAudioDelayBuffer_ClampMax(t *testing.T) {
	buf := NewAudioDelayBuffer(600)
	assert.Equal(t, 500, buf.DelayMs())
}

func TestAudioDelayBuffer_ClampMin(t *testing.T) {
	buf := NewAudioDelayBuffer(-10)
	assert.Equal(t, 0, buf.DelayMs())
}

func TestAudioDelayBuffer_SetDelayClamp(t *testing.T) {
	buf := NewAudioDelayBuffer(100)

	buf.SetDelayMs(999)
	assert.Equal(t, 500, buf.DelayMs())

	buf.SetDelayMs(-5)
	assert.Equal(t, 0, buf.DelayMs())
}

func TestAudioDelayBuffer_MultipleFrames(t *testing.T) {
	buf := NewAudioDelayBuffer(100)
	now := time.Now()

	// Ingest 5 frames at 20ms intervals
	for i := 0; i < 5; i++ {
		f := &media.AudioFrame{Data: []byte{byte(i)}, PTS: int64(i * 100)}
		buf.ingestAt(f, now.Add(time.Duration(i*20)*time.Millisecond))
	}

	// After 100ms, first frame should be available
	f := &media.AudioFrame{Data: []byte{5}, PTS: 500}
	out := buf.ingestAt(f, now.Add(100*time.Millisecond))
	require.NotNil(t, out)
	assert.Equal(t, byte(0), out.Data[0], "first frame should come out")
}

func TestAudioDelayBuffer_DrainOnZero(t *testing.T) {
	// When delay is set to 0, frames pass through immediately.
	buf := NewAudioDelayBuffer(0)
	now := time.Now()

	for i := 0; i < 3; i++ {
		f := &media.AudioFrame{Data: []byte{byte(i)}, PTS: int64(i * 100)}
		out := buf.ingestAt(f, now.Add(time.Duration(i*20)*time.Millisecond))
		require.NotNil(t, out, "frame %d should pass through immediately at 0ms delay", i)
		assert.Equal(t, byte(i), out.Data[0])
	}
}

func TestAudioDelayBuffer_SequentialOutput(t *testing.T) {
	// Verify frames come out in FIFO order.
	buf := NewAudioDelayBuffer(40)
	now := time.Now()

	// Ingest 4 frames at 20ms intervals (0, 20, 40, 60ms)
	frames := make([]*media.AudioFrame, 4)
	for i := 0; i < 4; i++ {
		frames[i] = &media.AudioFrame{Data: []byte{byte(i)}, PTS: int64(i * 1000)}
		out := buf.ingestAt(frames[i], now.Add(time.Duration(i*20)*time.Millisecond))
		if i < 2 {
			assert.Nil(t, out, "frame %d should be buffered", i)
		} else {
			require.NotNil(t, out, "frame %d should release a buffered frame", i)
			assert.Equal(t, byte(i-2), out.Data[0], "FIFO order")
		}
	}
}
