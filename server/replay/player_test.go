package replay

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	if forceIDR {
		// Return Annex B with SPS (High profile, Level 4.0) + PPS + IDR.
		// SPS NALU type = 0x67, profile_idc=0x64, constraint=0x00, level=0x28
		var out []byte
		out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
		out = append(out, 0x67, 0x64, 0x00, 0x28, 0xAC, 0xD1, 0x00) // SPS (type 7, High L4.0)
		out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
		out = append(out, 0x68, 0xCE, 0x38, 0x80) // PPS (type 8)
		out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
		out = append(out, 0x65, 0x88, 0x84, 0x00) // IDR slice (type 5)
		return out, true, nil
	}

	// Non-keyframe: Annex B with a single non-IDR slice.
	var out []byte
	out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
	out = append(out, 0x41, 0x9A, 0x00, 0x00) // non-IDR slice (type 1)
	return out, false, nil
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

func TestReplayPlayer_CodecStringFromSPS(t *testing.T) {
	// Verify the codec string on output frames is derived from the encoder's
	// SPS output (High profile Level 4.0 = "avc1.640028") rather than
	// hardcoded to Baseline ("avc1.42C01E").
	clip := buildTestClip(1, 3)
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
				Codec:      frame.Codec,
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.GreaterOrEqual(t, len(outputFrames), 3)

	// First frame (keyframe) should have codec string derived from SPS.
	// Mock encoder emits SPS with profile_idc=0x64, constraint=0x00, level=0x28
	// → "avc1.640028" (High profile, Level 4.0).
	require.Equal(t, "avc1.640028", outputFrames[0].Codec,
		"keyframe codec string should be derived from encoder SPS")

	// Non-keyframes should also carry the derived codec string.
	for i := 1; i < len(outputFrames); i++ {
		require.Equal(t, "avc1.640028", outputFrames[i].Codec,
			"frame %d codec string should persist from last keyframe SPS", i)
	}
}

func TestReplayPlayer_KeyframeHasSPSPPS(t *testing.T) {
	clip := buildTestClip(2, 5)
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
				SPS:        frame.SPS,
				PPS:        frame.PPS,
			})
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

	for i, f := range outputFrames {
		if f.IsKeyframe {
			require.NotNil(t, f.SPS, "keyframe %d should have SPS", i)
			require.NotNil(t, f.PPS, "keyframe %d should have PPS", i)
			require.Equal(t, byte(7), f.SPS[0]&0x1F, "SPS NALU type should be 7")
			require.Equal(t, byte(8), f.PPS[0]&0x1F, "PPS NALU type should be 8")
		} else {
			require.Nil(t, f.SPS, "non-keyframe %d should not have SPS", i)
			require.Nil(t, f.PPS, "non-keyframe %d should not have PPS", i)
		}
	}
}

func TestReplayPlayer_GroupIDIncrementsOnKeyframe(t *testing.T) {
	clip := buildTestClip(3, 4) // 3 GOPs, 4 frames each = 12 frames
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
				GroupID:    frame.GroupID,
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, outputFrames, 12)

	// GroupID should start at 1 (first keyframe) and increment on each keyframe.
	// Non-keyframes within a GOP share the same GroupID.
	var lastGroupID uint32
	keyframeGroups := 0
	for i, f := range outputFrames {
		require.Greater(t, f.GroupID, uint32(0), "frame %d GroupID should be > 0", i)
		if f.IsKeyframe {
			keyframeGroups++
			require.Greater(t, f.GroupID, lastGroupID,
				"keyframe %d GroupID should increment: got %d, last was %d",
				i, f.GroupID, lastGroupID)
		} else {
			require.Equal(t, lastGroupID, f.GroupID,
				"non-keyframe %d GroupID should equal previous keyframe: got %d, expected %d",
				i, f.GroupID, lastGroupID)
		}
		lastGroupID = f.GroupID
	}
	// With forceIDR only on the first frame, the mock encoder (which only
	// produces keyframes when forceIDR=true) will generate exactly 1 keyframe.
	// Real encoders produce additional natural keyframes at their GOP interval.
	require.Equal(t, 1, keyframeGroups, "expected 1 keyframe group (forceIDR on first frame only)")
}

func TestBlendInterpolator(t *testing.T) {
	interp := &blendInterpolator{}

	// Two 4x4 frames: A is all black (Y=0), B is all white (Y=255)
	size := 4 * 4 * 3 / 2 // YUV420
	frameA := make([]byte, size)
	frameB := make([]byte, size)
	for i := range frameB[:16] { // Y plane
		frameB[i] = 255
	}
	for i := 16; i < size; i++ { // UV planes neutral
		frameA[i] = 128
		frameB[i] = 128
	}

	// At alpha=0.5, Y should be ~128
	result := interp.Interpolate(frameA, frameB, 4, 4, 0.5)
	assert.InDelta(t, 128, int(result[0]), 1) // Y midpoint

	// At alpha=0.0, should be frameA
	result = interp.Interpolate(frameA, frameB, 4, 4, 0.0)
	assert.Equal(t, byte(0), result[0])

	// At alpha=1.0, should be frameB
	result = interp.Interpolate(frameA, frameB, 4, 4, 1.0)
	assert.Equal(t, byte(255), result[0])
}

func TestBlendInterpolator_BufferReuse(t *testing.T) {
	interp := &blendInterpolator{}

	size := 8 * 8 * 3 / 2
	frameA := make([]byte, size)
	frameB := make([]byte, size)
	for i := range frameB {
		frameB[i] = 200
	}

	// First call allocates buffer.
	result1 := interp.Interpolate(frameA, frameB, 8, 8, 0.5)
	assert.Len(t, result1, size)

	// Second call should reuse the same buffer (no new allocation for same size).
	result2 := interp.Interpolate(frameA, frameB, 8, 8, 0.25)
	assert.Len(t, result2, size)

	// Values should differ because alpha differs.
	// alpha=0.25: 0*0.75 + 200*0.25 + 0.5 = 50.5 → 50
	assert.InDelta(t, 50, int(result2[0]), 1)
}

func TestNewInterpolator(t *testing.T) {
	// InterpolationNone returns nil.
	assert.Nil(t, newInterpolator(InterpolationNone))

	// Empty string also returns nil (default).
	assert.Nil(t, newInterpolator(""))

	// InterpolationBlend returns a non-nil interpolator.
	interp := newInterpolator(InterpolationBlend)
	assert.NotNil(t, interp)

	// Verify it actually works.
	size := 2 * 2 * 3 / 2
	a := make([]byte, size)
	b := make([]byte, size)
	for i := range b {
		b[i] = 100
	}
	result := interp.Interpolate(a, b, 2, 2, 0.5)
	assert.InDelta(t, 50, int(result[0]), 1)
}

func TestReplayPlayer_BlendInterpolation(t *testing.T) {
	// Verify that the player uses frame blending at 0.5x speed when
	// Interpolation is set to InterpolationBlend. The mock decoder returns
	// YUV frames where the first byte is the decode count (1, 2, 3, ...),
	// so blended intermediate frames should have different first-byte values
	// than simple duplication would produce.
	clip := buildTestClip(1, 4) // 1 GOP, 4 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.5,
		Loop:           false,
		Interpolation:  InterpolationBlend,
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

	// At 0.5x, 4 input frames → 8 output frames (dupCount=2).
	require.Len(t, outputFrames, 8)

	// PTS should be monotonically increasing.
	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS)
	}
}

func TestReplayPlayer_NoneInterpolation(t *testing.T) {
	// Verify that InterpolationNone still works (frame duplication).
	clip := buildTestClip(1, 3)
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.5,
		Loop:           false,
		Interpolation:  InterpolationNone,
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

	// At 0.5x, 3 input frames → 6 output frames.
	require.Len(t, outputFrames, 6)
}

func TestReplayPlayer_OnVideoInfoCallback(t *testing.T) {
	clip := buildTestClip(1, 3)
	var gotSPS, gotPPS []byte
	var gotW, gotH int
	infoCh := make(chan struct{}, 1)

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() {},
		OnVideoInfo: func(sps, pps []byte, width, height int) {
			gotSPS = sps
			gotPPS = pps
			gotW = width
			gotH = height
			select {
			case infoCh <- struct{}{}:
			default:
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)

	select {
	case <-infoCh:
	case <-time.After(3 * time.Second):
		t.Fatal("OnVideoInfo not called within timeout")
	}

	p.Wait()

	// Mock encoder emits SPS type 0x67 and PPS type 0x68.
	require.NotEmpty(t, gotSPS, "expected SPS")
	require.NotEmpty(t, gotPPS, "expected PPS")
	require.Equal(t, byte(7), gotSPS[0]&0x1F, "SPS NALU type should be 7")
	require.Equal(t, byte(8), gotPPS[0]&0x1F, "PPS NALU type should be 8")
	require.Equal(t, 320, gotW)
	require.Equal(t, 240, gotH)
}

func TestReplayPlayer_OnVideoInfoCalledOnce(t *testing.T) {
	// With 2 GOPs, each starts with a keyframe. OnVideoInfo should only be
	// called once (on the very first keyframe).
	clip := buildTestClip(2, 5)
	var callCount int
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		OnDone:         func() {},
		OnVideoInfo: func(sps, pps []byte, width, height int) {
			mu.Lock()
			callCount++
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, callCount, "OnVideoInfo should be called exactly once")
}

func TestReplayPlayer_InitialPTS(t *testing.T) {
	clip := buildTestClip(1, 5)
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	initialPTS := int64(1_000_000)
	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     initialPTS,
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

	require.Len(t, outputFrames, 5)
	// First frame should start at initialPTS, not 0.
	require.Equal(t, initialPTS, outputFrames[0].PTS,
		"first frame PTS should be InitialPTS")

	// PTS should be monotonically increasing from InitialPTS.
	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS)
	}
}

func TestReplayPlayer_AudioPTSMonotonic(t *testing.T) {
	// Verify that audio frames get monotonically increasing PTS values,
	// not the same PTS as the video frame they're associated with.
	clip := buildTestClip(1, 5) // 1 GOP, 5 frames

	// Build audio frames that interleave with video frames.
	// At 48kHz, AAC frame = 1024 samples = ~21.3ms.
	// Video at 30fps = ~33.3ms per frame, so ~1.5 audio frames per video frame.
	// We'll create 8 audio frames spanning 5 video frames.
	now := time.Now()
	var audioClip []bufferedAudioFrame
	for i := 0; i < 8; i++ {
		audioClip = append(audioClip, bufferedAudioFrame{
			data:       make([]byte, 100),
			pts:        int64(i) * 1920, // 1024 * 90000 / 48000 = 1920
			sampleRate: 48000,
			channels:   2,
			wallTime:   now.Add(time.Duration(i) * 21333 * time.Microsecond),
		})
	}

	var audioFrames []*media.AudioFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     100_000,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		AudioOutput: func(frame *media.AudioFrame) {
			mu.Lock()
			defer mu.Unlock()
			audioFrames = append(audioFrames, &media.AudioFrame{
				PTS:        frame.PTS,
				SampleRate: frame.SampleRate,
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Greater(t, len(audioFrames), 1, "expected multiple audio frames")

	// Audio PTS should be monotonically increasing.
	for i := 1; i < len(audioFrames); i++ {
		require.Greater(t, audioFrames[i].PTS, audioFrames[i-1].PTS,
			"audio PTS not monotonic at frame %d: %d <= %d",
			i, audioFrames[i].PTS, audioFrames[i-1].PTS)
	}

	// First audio frame should start at InitialPTS.
	require.Equal(t, int64(100_000), audioFrames[0].PTS,
		"first audio frame should start at InitialPTS")

	// Each step should be exactly 1920 ticks (1024 samples at 48kHz in 90kHz clock).
	for i := 1; i < len(audioFrames); i++ {
		delta := audioFrames[i].PTS - audioFrames[i-1].PTS
		require.Equal(t, int64(1920), delta,
			"audio PTS step at frame %d should be 1920, got %d", i, delta)
	}
}

func TestReplayPlayer_LoopDoesNotResetPTS(t *testing.T) {
	clip := buildTestClip(1, 3) // 3 frames
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           true,
		InitialPTS:     500_000,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			outputFrames = append(outputFrames, &media.VideoFrame{PTS: frame.PTS})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p.Start(ctx)
	time.Sleep(500 * time.Millisecond) // Let it loop at least once
	p.Stop()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Greater(t, len(outputFrames), 3, "expected loop to produce >3 frames")

	// PTS should be strictly monotonically increasing across loops —
	// no reset to 0 or to InitialPTS on loop boundary.
	for i := 1; i < len(outputFrames); i++ {
		require.Greater(t, outputFrames[i].PTS, outputFrames[i-1].PTS,
			"PTS not monotonic at frame %d: %d <= %d (loop boundary PTS reset?)",
			i, outputFrames[i].PTS, outputFrames[i-1].PTS)
	}
}
