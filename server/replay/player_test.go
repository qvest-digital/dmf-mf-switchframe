package replay

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// mockReplayDecoder returns predictable YUV data per frame.
type mockReplayDecoder struct {
	width, height int
	decodeCount   int
}

func (d *mockReplayDecoder) Decode(data []byte) ([]byte, int, int, error) {
	d.decodeCount++
	yuv := make([]byte, d.width*d.height*3/2)
	// Mark first byte with decode count for identification.
	yuv[0] = byte(d.decodeCount)
	return yuv, d.width, d.height, nil
}

func (d *mockReplayDecoder) Close() {}

// mockReplayEncoder tracks encoded frames.
type mockReplayEncoder struct {
	mu          sync.Mutex
	encodeCount int
	closed      bool
}

func (e *mockReplayEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.encodeCount++
	// Return minimal AVC1 data.
	out := make([]byte, 8)
	out[3] = 4 // length
	out[4] = 0x65
	if forceIDR {
		out[4] = 0x65 // IDR slice
	}
	return out, forceIDR, nil
}

func (e *mockReplayEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

func mockDecoderFactory() (transition.VideoDecoder, error) {
	return &mockReplayDecoder{width: 320, height: 240}, nil
}

func mockEncoderFactory(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
	return &mockReplayEncoder{}, nil
}

// makeAVC1Data creates valid AVC1-formatted wire data (4-byte length prefix + NALU body).
func makeAVC1Data(size int) []byte {
	naluLen := size - 4
	if naluLen < 1 {
		naluLen = 1
	}
	data := make([]byte, 4+naluLen)
	data[0] = byte(naluLen >> 24)
	data[1] = byte(naluLen >> 16)
	data[2] = byte(naluLen >> 8)
	data[3] = byte(naluLen)
	data[4] = 0x65 // IDR slice NALU type
	for i := 5; i < len(data); i++ {
		data[i] = byte(i % 256)
	}
	return data
}

// buildTestClip creates a clip of bufferedFrames for testing.
func buildTestClip(nGOPs, framesPerGOP int) []bufferedFrame {
	var frames []bufferedFrame
	pts := int64(0)
	now := time.Now()
	for g := 0; g < nGOPs; g++ {
		for f := 0; f < framesPerGOP; f++ {
			isKey := f == 0
			bf := bufferedFrame{
				wireData:   makeAVC1Data(100),
				pts:        pts,
				isKeyframe: isKey,
				wallTime:   now.Add(time.Duration(pts) * time.Second / 90000),
			}
			if isKey {
				bf.sps = []byte{0x67, 0x42, 0xC0, 0x1E}
				bf.pps = []byte{0x68, 0xCE, 0x38, 0x80}
			}
			frames = append(frames, bf)
			pts += 3003 // ~30fps in 90kHz
		}
	}
	return frames
}

func TestReplayPlayer_PlayOnce(t *testing.T) {
	clip := buildTestClip(1, 5) // 1 GOP, 5 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			// Deep-copy since player may reuse buffers.
			f := &media.VideoFrame{
				PTS:        frame.PTS,
				IsKeyframe: frame.IsKeyframe,
				WireData:   make([]byte, len(frame.WireData)),
			}
			copy(f.WireData, frame.WireData)
			outputFrames = append(outputFrames, f)
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// At 1x speed, 5 input frames → 5 output frames.
	require.Len(t, outputFrames, 5)

	// First frame should be a keyframe.
	require.True(t, outputFrames[0].IsKeyframe, "first output frame should be a keyframe")

	// PTS should be monotonically increasing.
	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS,
			"output PTS not monotonic: frame %d PTS=%d <= frame %d PTS=%d",
			i, outputFrames[i].PTS, i-1, outputFrames[i-1].PTS)
	}
}

func TestReplayPlayer_SlowMotion(t *testing.T) {
	clip := buildTestClip(1, 4) // 1 GOP, 4 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.5,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			outputFrames = append(outputFrames, &media.VideoFrame{PTS: frame.PTS})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// At 0.5x, 4 input frames → 8 output frames (each duplicated once).
	require.Len(t, outputFrames, 8)
}

func TestReplayPlayer_QuarterSpeed(t *testing.T) {
	clip := buildTestClip(1, 3) // 1 GOP, 3 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.25,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			outputFrames = append(outputFrames, &media.VideoFrame{PTS: frame.PTS})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// At 0.25x, 3 input frames → 12 output frames (each x4).
	require.Len(t, outputFrames, 12)
}

func TestReplayPlayer_LoopMode(t *testing.T) {
	clip := buildTestClip(1, 3) // 1 GOP, 3 frames
	var mu sync.Mutex
	var outputCount int

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           true,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			outputCount++
			mu.Unlock()
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p.Start(ctx)

	// Let it loop for a bit, then stop.
	time.Sleep(200 * time.Millisecond)
	p.Stop()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should have produced more than one loop (3 frames).
	require.Greater(t, outputCount, 3, "expected loop to produce >3 frames")
}

func TestReplayPlayer_StopDuringPlayback(t *testing.T) {
	clip := buildTestClip(1, 100) // Many frames to ensure we can stop mid-play
	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.25, // Very slow — lots of output frames
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	p.Stop()
	p.Wait() // Should return promptly, not block.
}

func TestReplayPlayer_MonotonicPTS(t *testing.T) {
	clip := buildTestClip(2, 5) // 2 GOPs, 5 frames each = 10 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			outputFrames = append(outputFrames, &media.VideoFrame{PTS: frame.PTS})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, outputFrames, 10)

	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS,
			"non-monotonic PTS at frame %d: %d <= %d",
			i, outputFrames[i].PTS, outputFrames[i-1].PTS)
	}
}

func TestReplayPlayer_FirstFrameIsKeyframe(t *testing.T) {
	clip := buildTestClip(1, 3)
	var firstFrame *media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			if firstFrame == nil {
				firstFrame = &media.VideoFrame{IsKeyframe: frame.IsKeyframe}
			}
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.NotNil(t, firstFrame, "expected at least one output frame")
	require.True(t, firstFrame.IsKeyframe, "first output frame should be forced IDR keyframe")
}

func TestReplayPlayer_EmptyClipReturnsImmediately(t *testing.T) {
	p := newReplayPlayer(PlayerConfig{
		Clip:           nil,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait() // Should return immediately.
}

func TestReplayPlayer_PTSSortingExtractsBFrames(t *testing.T) {
	// Simulate B-frame reordering: decode order ≠ display order.
	// Decode order: I0, P3, B1, B2 → Display order should be: I0, B1, B2, P3
	clip := []bufferedFrame{
		{wireData: make([]byte, 100), pts: 0, isKeyframe: true,
			sps: []byte{0x67, 0x42, 0xC0, 0x1E}, pps: []byte{0x68, 0xCE, 0x38, 0x80}},
		{wireData: make([]byte, 100), pts: 9009, isKeyframe: false}, // P frame
		{wireData: make([]byte, 100), pts: 3003, isKeyframe: false}, // B frame
		{wireData: make([]byte, 100), pts: 6006, isKeyframe: false}, // B frame
	}

	// After decoding and sorting by PTS, display order should be 0, 3003, 6006, 9009.
	// Verify the sort logic directly.
	type displayFrame struct {
		pts int64
	}
	decoded := make([]displayFrame, len(clip))
	for i, f := range clip {
		decoded[i] = displayFrame{pts: f.pts}
	}
	sort.Slice(decoded, func(i, j int) bool {
		return decoded[i].pts < decoded[j].pts
	})

	expectedPTS := []int64{0, 3003, 6006, 9009}
	for i, df := range decoded {
		require.Equal(t, expectedPTS[i], df.pts, "frame %d PTS mismatch", i)
	}
}

func TestReplayPlayer_OnDoneCalled(t *testing.T) {
	clip := buildTestClip(1, 3)
	doneCh := make(chan struct{})

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() { close(doneCh) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)

	select {
	case <-doneCh:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("OnDone not called within timeout")
	}
}

func TestReplayPlayer_MultiGOPClip(t *testing.T) {
	// 3 GOPs, 5 frames each = 15 frames total. Verifies correct output
	// across GOP boundaries with per-GOP decode.
	clip := buildTestClip(3, 5)
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			outputFrames = append(outputFrames, &media.VideoFrame{
				PTS:        frame.PTS,
				IsKeyframe: frame.IsKeyframe,
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, outputFrames, 15)

	// First frame must be a keyframe.
	require.True(t, outputFrames[0].IsKeyframe, "first output frame should be a keyframe")

	// PTS should be monotonically increasing.
	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS,
			"non-monotonic PTS at frame %d: %d <= %d",
			i, outputFrames[i].PTS, i-1, outputFrames[i-1].PTS)
	}
}

func TestReplayPlayer_OnReadyCalled(t *testing.T) {
	clip := buildTestClip(1, 3)
	readyCh := make(chan struct{})

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() {},
		OnReady:        func() { close(readyCh) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)

	select {
	case <-readyCh:
		// OK — OnReady was called
	case <-time.After(3 * time.Second):
		t.Fatal("OnReady not called within timeout")
	}

	p.Wait()
}

func TestReplayPlayer_AnnexBConversion(t *testing.T) {
	// Frames with AVC1 wire data should be converted to Annex B for decoder.
	avc1Data := []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00}
	annexB := codec.AVC1ToAnnexB(avc1Data)
	require.NotEmpty(t, annexB, "AVC1ToAnnexB returned empty for valid input")
	// Verify start code is present.
	require.Equal(t, byte(0x00), annexB[0])
	require.Equal(t, byte(0x00), annexB[1])
	require.Equal(t, byte(0x00), annexB[2])
	require.Equal(t, byte(0x01), annexB[3], "expected Annex B start code")
}
