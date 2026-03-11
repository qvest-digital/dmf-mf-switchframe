package audio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestDelayBuffer_ZeroDelay(t *testing.T) {
	buf := NewDelayBuffer(0)
	frame := &media.AudioFrame{Data: []byte{1, 2, 3}, PTS: 100}
	out := buf.Ingest(frame)
	require.NotNil(t, out)
	assert.Equal(t, frame, out)
}

func TestDelayBuffer_DelayedOutput(t *testing.T) {
	buf := NewDelayBuffer(50)
	now := time.Now()

	f1 := &media.AudioFrame{Data: []byte{1}, PTS: 100}
	out := buf.ingestAt(f1, now)
	assert.Nil(t, out, "should not output before delay elapsed")

	f2 := &media.AudioFrame{Data: []byte{2}, PTS: 200}
	out = buf.ingestAt(f2, now.Add(50*time.Millisecond))
	require.NotNil(t, out)
	assert.Equal(t, f1.Data, out.Data, "should output first frame after delay")
}

func TestDelayBuffer_SetDelay(t *testing.T) {
	buf := NewDelayBuffer(0)
	buf.SetDelayMs(100)
	assert.Equal(t, 100, buf.DelayMs())
}

func TestDelayBuffer_ClampMax(t *testing.T) {
	buf := NewDelayBuffer(600)
	assert.Equal(t, 500, buf.DelayMs())
}

func TestDelayBuffer_ClampMin(t *testing.T) {
	buf := NewDelayBuffer(-10)
	assert.Equal(t, 0, buf.DelayMs())
}

func TestDelayBuffer_SetDelayClamp(t *testing.T) {
	buf := NewDelayBuffer(100)

	buf.SetDelayMs(999)
	assert.Equal(t, 500, buf.DelayMs())

	buf.SetDelayMs(-5)
	assert.Equal(t, 0, buf.DelayMs())
}

func TestDelayBuffer_MultipleFrames(t *testing.T) {
	buf := NewDelayBuffer(100)
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

func TestDelayBuffer_DrainOnZero(t *testing.T) {
	// When delay is set to 0, frames pass through immediately.
	buf := NewDelayBuffer(0)
	now := time.Now()

	for i := 0; i < 3; i++ {
		f := &media.AudioFrame{Data: []byte{byte(i)}, PTS: int64(i * 100)}
		out := buf.ingestAt(f, now.Add(time.Duration(i*20)*time.Millisecond))
		require.NotNil(t, out, "frame %d should pass through immediately at 0ms delay", i)
		assert.Equal(t, byte(i), out.Data[0])
	}
}

func TestDelayBuffer_SequentialOutput(t *testing.T) {
	// Verify frames come out in FIFO order.
	buf := NewDelayBuffer(40)
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

func TestDelayRingBufferWrap(t *testing.T) {
	// Fill past the ring capacity and verify correct FIFO behavior.
	buf := NewDelayBuffer(10)
	now := time.Now()

	// Ingest audioDelayRingSize+10 frames at 1ms intervals.
	// Each frame added after the delay elapses should produce the
	// oldest buffered frame in FIFO order.
	totalFrames := audioDelayRingSize + 10
	var outputs []*media.AudioFrame
	for i := 0; i < totalFrames; i++ {
		f := &media.AudioFrame{Data: []byte{byte(i)}, PTS: int64(i * 100)}
		out := buf.ingestAt(f, now.Add(time.Duration(i*5)*time.Millisecond))
		if out != nil {
			outputs = append(outputs, out)
		}
	}

	// Verify we got frames out and they're in FIFO order.
	require.NotEmpty(t, outputs, "should have released frames")
	for i := 1; i < len(outputs); i++ {
		require.Greater(t, outputs[i].PTS, outputs[i-1].PTS,
			"frame %d PTS (%d) should be > frame %d PTS (%d)",
			i, outputs[i].PTS, i-1, outputs[i-1].PTS)
	}
}

func TestDelayRingBufferNoLeak(t *testing.T) {
	// Process many frames and verify the internal ring stays bounded.
	buf := NewDelayBuffer(20)
	now := time.Now()

	for i := 0; i < 10000; i++ {
		f := &media.AudioFrame{Data: []byte{byte(i & 0xFF)}, PTS: int64(i * 100)}
		buf.ingestAt(f, now.Add(time.Duration(i)*time.Millisecond))
	}

	// The ring buffer has a fixed size; count should never exceed it.
	buf.mu.Lock()
	require.LessOrEqual(t, buf.count, audioDelayRingSize,
		"ring count %d exceeds capacity %d", buf.count, audioDelayRingSize)
	buf.mu.Unlock()
}
