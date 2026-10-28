package clip

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threadedDecoder behaves like a frame-threaded decoder: it returns a
// picture only once depth frames are in flight, each call takes cost, and
// Flush throws away what it holds, so it returns nothing again for its
// next depth calls. The picture is the frame's last byte.
type threadedDecoder struct {
	depth int
	cost  time.Duration
	queue [][]byte
	eos   bool
}

func (d *threadedDecoder) Decode(annexB []byte) ([]byte, int, int, error) {
	time.Sleep(d.cost)
	d.queue = append(d.queue, []byte{annexB[len(annexB)-1]})
	if len(d.queue) <= d.depth {
		return nil, 0, 0, errors.New("no output frame yet (buffering)")
	}
	out := d.queue[0]
	d.queue = d.queue[1:]
	return out, 16, 16, nil
}

func (d *threadedDecoder) SendEOS() error { d.eos = true; return nil }

func (d *threadedDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if !d.eos || len(d.queue) == 0 {
		d.eos = false
		return nil, 0, 0, errors.New("no more frames")
	}
	out := d.queue[0]
	d.queue = d.queue[1:]
	return out, 16, 16, nil
}

func (d *threadedDecoder) Flush() { d.queue, d.eos = nil, false }
func (d *threadedDecoder) Close() {}

// A looping clip keeps its cadence across the wrap: every picture once per
// pass, one output period apart, audio included. Restarting the decoder at
// the wrap held the last picture while a frame-threaded decoder refilled,
// and left the slots of that time without audio.
func TestPlayerLoopWrapKeepsCadence(t *testing.T) {
	const pictures = 20
	clip := make([]BufferedFrame, pictures)
	audio := make([]BufferedAudioFrame, pictures)
	for i := range clip {
		// Length-prefixed NAL {0x41, i}: the decoder sees i as its last byte.
		clip[i] = BufferedFrame{wireData: []byte{0, 0, 0, 2, 0x41, byte(i)}, pts: int64(i) * 1800, isKeyframe: i == 0}
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, sampleRate: 48000, channels: 2}
	}
	dec := &threadedDecoder{depth: 4, cost: 6 * time.Millisecond}

	var mu sync.Mutex
	var shown []byte
	var at []time.Time
	audioOut := 0
	p := NewPlayer(PlayerConfig{
		Clip:         clip,
		AudioClip:    audio,
		Speed:        1,
		Loop:         true,
		OutputFPSNum: 50,
		OutputFPSDen: 1,
		Decoder:      dec,
		DecodeFrame:  dec.Decode,
		RawVideoOutput: func(yuv []byte, _, _ int, _ int64, _ bool) {
			mu.Lock()
			shown = append(shown, yuv[0])
			at = append(at, time.Now())
			mu.Unlock()
		},
		AudioOutput: func([]byte, int64, int, int) {
			mu.Lock()
			audioOut++
			mu.Unlock()
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	// Two wraps: 2.5 passes of 20 pictures at 50 fps.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(shown) >= 2*pictures+pictures/2
	}, 5*time.Second, 5*time.Millisecond)
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, v := range shown[:2*pictures] {
		require.Equal(t, byte(i%pictures), v, "picture %d of %v", i, shown)
	}
	period := 20 * time.Millisecond
	for i := 1; i < len(at); i++ {
		gap := at[i].Sub(at[i-1])
		assert.LessOrEqual(t, gap, period*3/2, "gap before output %d (picture %d)", i, shown[i])
		assert.GreaterOrEqual(t, gap, period/2, "gap before output %d (picture %d)", i, shown[i])
	}
	assert.GreaterOrEqual(t, audioOut, 2*pictures, "audio frames emitted over two passes")
}
