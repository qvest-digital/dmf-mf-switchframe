package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRingBuffer_LatencyMono(t *testing.T) {
	const sampleRate = 48000
	rb := NewPCMRingBufferWithChannels(4096, 1)
	pcm := make([]float32, 1024)
	rb.Push(pcm)

	assert.Equal(t, 1024, rb.Len(), "Len returns interleaved samples")
	assert.Equal(t, 1024, rb.FrameCount(), "mono: 1024 samples = 1024 frames")

	latency90k := int64(rb.FrameCount()) * 90000 / int64(sampleRate)
	assert.Equal(t, int64(1920), latency90k, "1024 frames at 48kHz = 1920 ticks")
}

func TestRingBuffer_LatencyStereo(t *testing.T) {
	const sampleRate = 48000
	rb := NewPCMRingBufferWithChannels(4096, 2)
	pcm := make([]float32, 2048)
	rb.Push(pcm)

	assert.Equal(t, 2048, rb.Len(), "Len returns interleaved samples")
	assert.Equal(t, 1024, rb.FrameCount(), "stereo: 2048 samples = 1024 frames")

	latency90k := int64(rb.FrameCount()) * 90000 / int64(sampleRate)
	assert.Equal(t, int64(1920), latency90k, "1024 frames at 48kHz = 1920 ticks")
}

func TestRingBuffer_Latency5_1(t *testing.T) {
	const sampleRate = 48000
	rb := NewPCMRingBufferWithChannels(8192, 6)
	pcm := make([]float32, 6144)
	rb.Push(pcm)

	assert.Equal(t, 6144, rb.Len(), "Len returns interleaved samples")
	assert.Equal(t, 1024, rb.FrameCount(), "5.1: 6144 samples = 1024 frames")

	latency90k := int64(rb.FrameCount()) * 90000 / int64(sampleRate)
	assert.Equal(t, int64(1920), latency90k, "1024 frames at 48kHz = 1920 ticks")
}

func TestRingBuffer_FrameCount_DefaultConstructorIsSampleCount(t *testing.T) {
	rb := NewPCMRingBuffer(4096)
	rb.Push(make([]float32, 1024))
	assert.Equal(t, 1024, rb.FrameCount(), "default constructor channels=1: frames == samples")
}
