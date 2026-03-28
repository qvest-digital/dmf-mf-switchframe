package clock

import "github.com/zsiec/prism/media"

const aacSamplesPerFrame = 1024

// AACFrameBuffer accumulates variable-length PCM chunks and emits complete
// 1024-sample AAC frames. Handles the mismatch between video frame rate
// (variable samples per tick) and AAC frame size (fixed 1024).
type AACFrameBuffer struct {
	buf        []float32
	writePos   int
	channels   int
	sampleRate int
	frameCount int64 // AAC frames emitted (for PTS computation)
	basePTS    int64 // PTS of first sample in the stream
	encodeFn   func(pcm []float32) ([]byte, error)
}

// NewAACFrameBuffer creates a buffer that accumulates interleaved PCM samples
// and emits fixed 1024-sample AAC frames via the provided encoder function.
func NewAACFrameBuffer(channels, sampleRate int, encodeFn func([]float32) ([]byte, error)) *AACFrameBuffer {
	// Pre-allocate for ~2 AAC frames worth of samples to reduce allocations.
	capacity := aacSamplesPerFrame * channels * 2
	return &AACFrameBuffer{
		buf:        make([]float32, capacity),
		channels:   channels,
		sampleRate: sampleRate,
		encodeFn:   encodeFn,
	}
}

// SetBasePTS sets the starting PTS for the audio stream.
func (b *AACFrameBuffer) SetBasePTS(pts int64) {
	b.basePTS = pts
}

// Feed adds interleaved PCM samples and returns any complete AAC frames.
// Each frame has PTS = basePTS + frameCount * frameDuration90k.
// frameDuration90k = 1024 * 90000 / sampleRate (= 1920 at 48kHz).
func (b *AACFrameBuffer) Feed(pcm []float32) ([]*media.AudioFrame, error) {
	if len(pcm) == 0 {
		return nil, nil
	}

	// Grow buffer if needed.
	needed := b.writePos + len(pcm)
	if needed > len(b.buf) {
		grown := make([]float32, needed*2)
		copy(grown, b.buf[:b.writePos])
		b.buf = grown
	}

	copy(b.buf[b.writePos:], pcm)
	b.writePos += len(pcm)

	return b.drain(false)
}

// Flush encodes remaining samples (padded with silence to a full 1024-sample
// frame). Intended for clean shutdown.
func (b *AACFrameBuffer) Flush() ([]*media.AudioFrame, error) {
	if b.writePos == 0 {
		return nil, nil
	}
	return b.drain(true)
}

// Pending returns the number of samples (per-channel) currently buffered.
func (b *AACFrameBuffer) Pending() int {
	if b.channels == 0 {
		return 0
	}
	return b.writePos / b.channels
}

// FrameCount returns the total number of AAC frames emitted.
func (b *AACFrameBuffer) FrameCount() int64 {
	return b.frameCount
}

// drain extracts complete 1024-sample frames from the buffer. If flush is true,
// the final partial frame is zero-padded and emitted.
func (b *AACFrameBuffer) drain(flush bool) ([]*media.AudioFrame, error) {
	frameSize := aacSamplesPerFrame * b.channels
	frameDuration90k := int64(aacSamplesPerFrame) * 90000 / int64(b.sampleRate)

	var frames []*media.AudioFrame

	for b.writePos >= frameSize {
		encoded, err := b.encodeFn(b.buf[:frameSize])
		if err != nil {
			return frames, err
		}

		frames = append(frames, &media.AudioFrame{
			PTS:        b.basePTS + b.frameCount*frameDuration90k,
			Data:       encoded,
			SampleRate: b.sampleRate,
			Channels:   b.channels,
		})
		b.frameCount++

		// Shift remaining data forward.
		copy(b.buf, b.buf[frameSize:b.writePos])
		b.writePos -= frameSize
	}

	if flush && b.writePos > 0 {
		// Pad remaining samples with silence to fill a complete frame.
		padded := make([]float32, frameSize)
		copy(padded, b.buf[:b.writePos])

		encoded, err := b.encodeFn(padded)
		if err != nil {
			return frames, err
		}

		frames = append(frames, &media.AudioFrame{
			PTS:        b.basePTS + b.frameCount*frameDuration90k,
			Data:       encoded,
			SampleRate: b.sampleRate,
			Channels:   b.channels,
		})
		b.frameCount++
		b.writePos = 0
	}

	return frames, nil
}
