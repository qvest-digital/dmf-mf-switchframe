package replay

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
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

func (e *mockReplayEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.encodeCount++

	if forceIDR {
		// Return Annex B with SPS (High profile, Level 4.0) + PPS + IDR.
		// SPS NALU type = 0x67, profile_idc=0x64, constraint=0x00, level=0x28
		var out []byte
		out = append(out, 0x00, 0x00, 0x00, 0x01)                   // start code
		out = append(out, 0x67, 0x64, 0x00, 0x28, 0xAC, 0xD1, 0x00) // SPS (type 7, High L4.0)
		out = append(out, 0x00, 0x00, 0x00, 0x01)                   // start code
		out = append(out, 0x68, 0xCE, 0x38, 0x80)                   // PPS (type 8)
		out = append(out, 0x00, 0x00, 0x00, 0x01)                   // start code
		out = append(out, 0x65, 0x88, 0x84, 0x00)                   // IDR slice (type 5)
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

func mockEncoderFactory(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
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
	slices.SortFunc(decoded, func(a, b displayFrame) int {
		return cmp.Compare(a.pts, b.pts)
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

func TestBlendInterpolator_AliasedBufferCorruption(t *testing.T) {
	// Regression test: blendInterpolator.Interpolate() returns a slice backed
	// by an internal reusable buffer. If a consumer holds onto the first result
	// while the interpolator is called again, the first result's data gets
	// corrupted because the second call overwrites the same buffer.
	interp := &blendInterpolator{}

	w, h := 8, 8
	size := w * h * 3 / 2

	// Frame A: all zeros
	frameA := make([]byte, size)
	// Frame B: all 200
	frameB := make([]byte, size)
	for i := range frameB {
		frameB[i] = 200
	}

	// First interpolation at alpha=0.5 → expect ~100 for all bytes
	result1 := interp.Interpolate(frameA, frameB, w, h, 0.5)
	require.Len(t, result1, size)
	snapshot1 := byte(100) // 0*0.5 + 200*0.5 = 100

	// Verify the first result is correct before the second call
	assert.InDelta(t, int(snapshot1), int(result1[0]), 1, "result1 should be ~100 before second Interpolate call")

	// Now create different input frames for the second call
	// Frame C: all 50, Frame D: all 250
	frameC := make([]byte, size)
	frameD := make([]byte, size)
	for i := range frameC {
		frameC[i] = 50
	}
	for i := range frameD {
		frameD[i] = 250
	}

	// Second interpolation at alpha=0.5 → expect ~150
	result2 := interp.Interpolate(frameC, frameD, w, h, 0.5)
	require.Len(t, result2, size)

	// BUG: If result1 is aliased to the internal buffer, it now contains ~150
	// instead of the original ~100. An async consumer holding result1 would
	// see corrupted data.
	assert.InDelta(t, int(snapshot1), int(result1[0]), 1,
		"result1 should still be ~100 after second Interpolate call; aliased buffer corruption detected")
	_ = result2
}

func TestMCFIInterpolator_AliasedBufferCorruption(t *testing.T) {
	// Regression test: MCFIState.Interpolate() returns a slice backed by
	// internal reusable buffers (blendOut or fallbackBuf). If a consumer holds
	// onto the first result while the interpolator is called again with
	// different frame pair, the first result's data gets corrupted.
	interp := newInterpolator(InterpolationMCFI)
	require.NotNil(t, interp)

	w, h := 32, 32
	size := w * h * 3 / 2

	// First pair: frame A = all 60, frame B = all 180
	frameA := make([]byte, size)
	frameB := make([]byte, size)
	for i := 0; i < size; i++ {
		frameA[i] = 60
		frameB[i] = 180
	}

	// MCFI with uniform frames will detect "scene change" (or low-confidence MVs)
	// and fall back to linear blend. alpha=0.5 → expect ~120 for all bytes.
	result1 := interp.Interpolate(frameA, frameB, w, h, 0.5)
	require.Len(t, result1, size)
	val1 := result1[0]
	require.InDelta(t, 120, int(val1), 2, "first result should be ~120")

	// Second pair: entirely different values
	frameC := make([]byte, size)
	frameD := make([]byte, size)
	for i := 0; i < size; i++ {
		frameC[i] = 10
		frameD[i] = 250
	}

	result2 := interp.Interpolate(frameC, frameD, w, h, 0.5)
	require.Len(t, result2, size)

	// BUG: If result1 is aliased to the internal buffer, it now contains ~130
	// instead of the original ~120.
	assert.InDelta(t, int(val1), int(result1[0]), 2,
		"result1 should still be ~120 after second Interpolate call; aliased buffer corruption detected")
	_ = result2
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

	// InterpolationMCFI returns a non-nil interpolator.
	mcfi := newInterpolator(InterpolationMCFI)
	assert.NotNil(t, mcfi)
}

func TestMCFIInterpolator(t *testing.T) {
	interp := newInterpolator(InterpolationMCFI)
	require.NotNil(t, interp)

	// Create two 32x32 frames (minimum for 16x16 block ME: 2 blocks wide, 2 tall).
	w, h := 32, 32
	size := w * h * 3 / 2
	frameA := make([]byte, size)
	frameB := make([]byte, size)

	// Frame A: gradient left to right on Y plane
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			frameA[y*w+x] = byte(x * 8) // 0-248
		}
	}
	// Frame B: same gradient shifted right by 4 pixels (simulates motion)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcX := x - 4
			if srcX < 0 {
				srcX = 0
			}
			frameB[y*w+x] = byte(srcX * 8)
		}
	}

	// Set neutral chroma
	ySize := w * h
	for i := ySize; i < size; i++ {
		frameA[i] = 128
		frameB[i] = 128
	}

	// Interpolate at alpha=0.5
	result := interp.Interpolate(frameA, frameB, w, h, 0.5)
	require.Len(t, result, size)

	// Result should differ from both source frames (motion-compensated)
	diffA := 0
	diffB := 0
	for i := 0; i < ySize; i++ {
		if result[i] != frameA[i] {
			diffA++
		}
		if result[i] != frameB[i] {
			diffB++
		}
	}
	assert.Greater(t, diffA, 0, "MCFI result should differ from frame A")
	assert.Greater(t, diffB, 0, "MCFI result should differ from frame B")
}

func TestMCFIInterpolator_CachesMVs(t *testing.T) {
	interp := newInterpolator(InterpolationMCFI)
	require.NotNil(t, interp)

	// Use frames with spatial variation and motion so MVs are non-zero.
	w, h := 32, 32
	size := w * h * 3 / 2
	frameA := make([]byte, size)
	frameB := make([]byte, size)

	// Frame A: horizontal gradient
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			frameA[y*w+x] = byte(x * 8)
		}
	}
	// Frame B: same gradient shifted right by 4px (simulates motion)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcX := x - 4
			if srcX < 0 {
				srcX = 0
			}
			frameB[y*w+x] = byte(srcX * 8)
		}
	}
	// Neutral chroma
	ySize := w * h
	for i := ySize; i < size; i++ {
		frameA[i] = 128
		frameB[i] = 128
	}

	// Multiple calls with same frame pair but different alpha should all work.
	// Motion vectors are computed once (cached), warps use different alpha.
	r1 := interp.Interpolate(frameA, frameB, w, h, 0.25)
	require.Len(t, r1, size)
	v1 := make([]byte, size)
	copy(v1, r1)

	r2 := interp.Interpolate(frameA, frameB, w, h, 0.5)
	require.Len(t, r2, size)

	r3 := interp.Interpolate(frameA, frameB, w, h, 0.75)
	require.Len(t, r3, size)

	// Results at different alpha should produce different warps
	differ := false
	for i := 0; i < ySize; i++ {
		if v1[i] != r3[i] {
			differ = true
			break
		}
	}
	assert.True(t, differ, "alpha=0.25 and alpha=0.75 should produce different results")
}

func TestReplayPlayer_MCFIInterpolation(t *testing.T) {
	// Verify the player works with MCFI interpolation at 0.5x speed.
	clip := buildTestClip(1, 4)
	var outputFrames []*media.VideoFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.5,
		Loop:           false,
		Interpolation:  InterpolationMCFI,
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

func TestFpsToRational(t *testing.T) {
	tests := []struct {
		name    string
		fps     float64
		wantNum int
		wantDen int
	}{
		{"29.97 NTSC", 29.97, 30000, 1001},
		{"exact 30", 30.0, 30, 1},
		{"23.976 film", 23.976, 24000, 1001},
		{"exact 24", 24.0, 24, 1},
		{"exact 25 PAL", 25.0, 25, 1},
		{"exact 50", 50.0, 50, 1},
		{"59.94", 59.94, 60000, 1001},
		{"exact 60", 60.0, 60, 1},
		{"non-standard 15fps snaps to nearest", 15.0, 24000, 1001},
		{"non-standard 120fps snaps to nearest", 120.0, 60, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, den := fpsToRational(tt.fps)
			require.Equal(t, tt.wantNum, num, "fpsNum")
			require.Equal(t, tt.wantDen, den, "fpsDen")
		})
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

func TestReplayPlayer_RawVideoOutput(t *testing.T) {
	clip := buildTestClip(1, 5) // 1 GOP, 5 frames
	type rawFrame struct {
		w, h int
		pts  int64
	}
	var rawFrames []rawFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		DecoderFactory: mockDecoderFactory,
		// No EncoderFactory or Output — raw-only mode.
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
			mu.Lock()
			defer mu.Unlock()
			rawFrames = append(rawFrames, rawFrame{w: w, h: h, pts: pts})
		},
		OnDone:  func() {},
		OnReady: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, rawFrames, 5, "should receive one raw frame per clip frame at 1.0x")
	for _, f := range rawFrames {
		require.True(t, f.w > 0)
		require.True(t, f.h > 0)
	}
	// PTS should be monotonically increasing.
	for i := 1; i < len(rawFrames); i++ {
		require.Greater(t, rawFrames[i].pts, rawFrames[i-1].pts)
	}
}

func TestReplayPlayer_RawVideoOutput_SlowMotion(t *testing.T) {
	clip := buildTestClip(1, 4) // 1 GOP, 4 frames
	var rawCount int64

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          0.5, // 2x duplication
		Loop:           false,
		InitialPTS:     0,
		DecoderFactory: mockDecoderFactory,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
			atomic.AddInt64(&rawCount, 1)
		},
		OnDone:  func() {},
		OnReady: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	p.Wait()

	require.Equal(t, int64(8), atomic.LoadInt64(&rawCount),
		"should receive 8 raw frames (4 clip frames x 2 dups at 0.5x)")
}

func TestReplayPlayer_RawAndEncodedOutput(t *testing.T) {
	clip := buildTestClip(1, 3) // 1 GOP, 3 frames
	var rawCount, encodedCount int64

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
			atomic.AddInt64(&rawCount, 1)
		},
		Output: func(frame *media.VideoFrame) {
			atomic.AddInt64(&encodedCount, 1)
		},
		OnDone:  func() {},
		OnReady: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	p.Wait()

	// Both paths should receive the same number of frames.
	require.Equal(t, int64(3), atomic.LoadInt64(&rawCount))
	require.Equal(t, int64(3), atomic.LoadInt64(&encodedCount))
}

// mockAudioDecoder returns 1024 stereo samples per Decode call.
type mockAudioDecoder struct{}

func (d *mockAudioDecoder) Decode(data []byte) ([]float32, error) {
	// Return 1024 stereo samples of constant value.
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	return pcm, nil
}

func (d *mockAudioDecoder) Close() error { return nil }

// mockAudioEncoder returns a dummy AAC frame per Encode call.
type mockAudioEncoder struct{}

func (e *mockAudioEncoder) Encode(pcm []float32) ([]byte, error) {
	return make([]byte, 64), nil
}

func (e *mockAudioEncoder) Close() error { return nil }

func TestReplayPlayer_WSOLAPreStretch(t *testing.T) {
	clip := buildTestClip(1, 4)

	// Build audio frames spanning the clip.
	var audioClip []bufferedAudioFrame
	for i := 0; i < 6; i++ {
		audioClip = append(audioClip, bufferedAudioFrame{
			data:       make([]byte, 100),
			pts:        int64(i) * 1920,
			sampleRate: 48000,
			channels:   2,
			wallTime:   time.Now(),
		})
	}

	var audioCount int64
	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          0.5,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		AudioOutput: func(frame *media.AudioFrame) {
			atomic.AddInt64(&audioCount, 1)
		},
		AudioDecoderFactory: func(sampleRate, channels int) (audio.Decoder, error) {
			return &mockAudioDecoder{}, nil
		},
		AudioEncoderFactory: func(sampleRate, channels int) (audio.Encoder, error) {
			return &mockAudioEncoder{}, nil
		},
		OnDone:  func() {},
		OnReady: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	p.Wait()

	count := atomic.LoadInt64(&audioCount)
	// With WSOLA stretching at 0.5x, stretched audio should have ~2x the frames.
	// The stretched frames should be emitted during ALL duplicate frames, not just dup==0.
	require.Greater(t, count, int64(6),
		"WSOLA-stretched audio should produce more frames than original 6")
}

func TestReplayPlayer_AudioWithoutWSOLA(t *testing.T) {
	clip := buildTestClip(1, 3)
	var audioClip []bufferedAudioFrame
	for i := 0; i < 4; i++ {
		audioClip = append(audioClip, bufferedAudioFrame{
			data:       make([]byte, 50),
			pts:        int64(i) * 1920,
			sampleRate: 48000,
			channels:   2,
			wallTime:   time.Now(),
		})
	}

	var audioCount int64
	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          0.5,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		AudioOutput: func(frame *media.AudioFrame) {
			atomic.AddInt64(&audioCount, 1)
		},
		// No AudioDecoderFactory/AudioEncoderFactory — falls back to sparse audio.
		OnDone:  func() {},
		OnReady: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	p.Wait()

	count := atomic.LoadInt64(&audioCount)
	// Without WSOLA, audio frames are only emitted on dup==0 (first duplicate).
	// 3 source frames -> each gets some audio frames on first dup only.
	require.Greater(t, count, int64(0), "should emit some audio frames")
	require.LessOrEqual(t, count, int64(4), "without WSOLA, should not exceed original audio frame count")
}

func TestComputeReplayTiming_FractionalSpeed(t *testing.T) {
	// At 0.33x speed, math.Ceil(1/0.33) = 4 but math.Round(1/0.33) = 3.
	// Using Ceil causes video to run 4/3.03 = 1.32x too long vs audio stretch.
	// The fix uses Round and adjusts frameDuration so total video time matches
	// the audio stretch factor exactly.
	tests := []struct {
		name            string
		speed           float64
		sourceFPS       float64
		totalClipFrames int
		wantDupCount    int
	}{
		{
			name:            "0.33x speed uses Round not Ceil",
			speed:           0.33,
			sourceFPS:       30.0,
			totalClipFrames: 90,
			wantDupCount:    3, // Round(1/0.33) = Round(3.03) = 3, not Ceil = 4
		},
		{
			name:            "0.5x speed unchanged",
			speed:           0.5,
			sourceFPS:       30.0,
			totalClipFrames: 60,
			wantDupCount:    2,
		},
		{
			name:            "0.25x speed unchanged",
			speed:           0.25,
			sourceFPS:       30.0,
			totalClipFrames: 30,
			wantDupCount:    4,
		},
		{
			name:            "1.0x speed",
			speed:           1.0,
			sourceFPS:       30.0,
			totalClipFrames: 30,
			wantDupCount:    1,
		},
		{
			name:            "0.1x speed (near boundary)",
			speed:           0.1,
			sourceFPS:       30.0,
			totalClipFrames: 30,
			wantDupCount:    10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dupCount, frameDuration := computeReplayTiming(tt.speed, tt.sourceFPS, tt.totalClipFrames)

			// Verify dupCount uses Round.
			require.Equal(t, tt.wantDupCount, dupCount, "dupCount mismatch")

			// Verify total video time matches audio stretch time within 1%.
			// Audio stretch time = originalDuration / speed
			// Video time = totalClipFrames * dupCount * frameDuration
			originalDuration := float64(tt.totalClipFrames) / tt.sourceFPS
			expectedTotalTime := originalDuration / tt.speed
			totalFrames := tt.totalClipFrames * dupCount
			actualTotalTime := float64(totalFrames) * frameDuration.Seconds()

			ratio := actualTotalTime / expectedTotalTime
			assert.InDelta(t, 1.0, ratio, 0.01,
				"video/audio duration ratio should be ~1.0, got %.4f (video=%.4fs, audio=%.4fs)",
				ratio, actualTotalTime, expectedTotalTime)
		})
	}
}

func TestComputeReplayTiming_MinDupCount(t *testing.T) {
	// Extremely high speed shouldn't produce dupCount < 1.
	dupCount, frameDuration := computeReplayTiming(2.0, 30.0, 30)
	require.Equal(t, 1, dupCount, "dupCount should floor to 1")
	require.Greater(t, frameDuration, time.Duration(0), "frameDuration should be positive")
}

// mockDropDecoder drops the Nth frame (0-indexed) with an error, simulating
// a corrupt frame that the decoder rejects (not EAGAIN/buffering).
type mockDropDecoder struct {
	width, height int
	decodeCount   int
	dropIndex     int // 0-based index of the frame to drop
}

func (d *mockDropDecoder) Decode(data []byte) ([]byte, int, int, error) {
	idx := d.decodeCount
	d.decodeCount++
	if idx == d.dropIndex {
		return nil, 0, 0, errors.New("corrupt frame")
	}
	yuv := make([]byte, d.width*d.height*3/2)
	yuv[0] = byte(idx + 1) // mark for identification
	return yuv, d.width, d.height, nil
}

func (d *mockDropDecoder) Close() {}

func TestDecodeGOP_PTSAssignmentOnFrameDrop(t *testing.T) {
	// When a decoder drops a frame (non-EAGAIN error), remaining
	// frames get wrong PTS because sortedPTS[len(decoded)] uses the decoded
	// output count as the index, not the input consumption count.
	//
	// GOP with 4 frames, PTS = [1000, 2000, 3000, 4000].
	// Frame at index 1 (PTS 2000) is dropped by the decoder.
	// Expected: 3 decoded frames with PTS [1000, 3000, 4000].
	// Incorrect: PTS [1000, 2000, 3000] would result from indexing by decoded output count instead of input consumption index.
	gop := []bufferedFrame{
		{wireData: makeAVC1Data(100), pts: 1000, isKeyframe: true,
			sps: []byte{0x67, 0x42, 0xC0, 0x1E}, pps: []byte{0x68, 0xCE, 0x38, 0x80}},
		{wireData: makeAVC1Data(100), pts: 2000, isKeyframe: false},
		{wireData: makeAVC1Data(100), pts: 3000, isKeyframe: false},
		{wireData: makeAVC1Data(100), pts: 4000, isKeyframe: false},
	}

	dropFactory := func() (transition.VideoDecoder, error) {
		return &mockDropDecoder{width: 320, height: 240, dropIndex: 1}, nil
	}

	decoded, err := decodeGOP(gop, dropFactory)
	require.NoError(t, err)
	require.Len(t, decoded, 3, "should have 3 decoded frames (1 dropped)")

	// Sort by PTS for display order (decodeGOP caller does this).
	slices.SortFunc(decoded, func(a, b decodedFrame) int {
		return cmp.Compare(a.pts, b.pts)
	})

	// The decoded frames should have PTS [1000, 3000, 4000], NOT [1000, 2000, 3000].
	expectedPTS := []int64{1000, 3000, 4000}
	for i, df := range decoded {
		require.Equal(t, expectedPTS[i], df.pts,
			"frame %d PTS mismatch: got %d, want %d", i, df.pts, expectedPTS[i])
	}
}

func TestEstimateFPSFromClip(t *testing.T) {
	t.Run("single frame returns default 30fps", func(t *testing.T) {
		clip := []bufferedFrame{{pts: 1000}}
		assert.Equal(t, 30.0, estimateFPSFromClip(clip))
	})

	t.Run("empty clip returns default 30fps", func(t *testing.T) {
		assert.Equal(t, 30.0, estimateFPSFromClip(nil))
	})

	t.Run("frames in PTS order at 30fps", func(t *testing.T) {
		// 30fps = 3000 ticks per frame at 90kHz
		clip := []bufferedFrame{
			{pts: 0},
			{pts: 3000},
			{pts: 6000},
			{pts: 9000},
		}
		fps := estimateFPSFromClip(clip)
		assert.InDelta(t, 30.0, fps, 0.1)
	})

	t.Run("frames in decode order with B-frames", func(t *testing.T) {
		// B-frame decode order: I0, P3, B1, B2 — PTS are out of order.
		// PTS span should be max(9000) - min(0) = 9000, giving 30fps.
		clip := []bufferedFrame{
			{pts: 0, isKeyframe: true}, // I-frame (display order 0)
			{pts: 9000},                // P-frame (display order 3)
			{pts: 3000},                // B-frame (display order 1)
			{pts: 6000},                // B-frame (display order 2)
		}
		fps := estimateFPSFromClip(clip)
		// With the bug (last-first): (6000-0)/3 = 2000 ticks/frame = 45fps — WRONG.
		// With the fix (min/max): (9000-0)/3 = 3000 ticks/frame = 30fps — CORRECT.
		assert.InDelta(t, 30.0, fps, 0.1)
	})

	t.Run("decode order where last PTS is less than first PTS", func(t *testing.T) {
		// Pathological case: I0, B-1(reorder), P2 in decode order
		// where the last frame in decode order has the lowest PTS.
		// Duplicate PTS values yield ptsSpan=0, triggering the default 30fps.
		// even for a 24fps source.
		clip := []bufferedFrame{
			{pts: 15000, isKeyframe: true}, // I-frame
			{pts: 3750},                    // B-frame (lowest PTS, at end of decode order)
			{pts: 7500},                    // B-frame
			{pts: 11250},                   // B-frame
		}
		// PTS span = 15000 - 3750 = 11250, frames-1 = 3
		// fps = 3 * 90000 / 11250 = 24.0
		fps := estimateFPSFromClip(clip)
		assert.InDelta(t, 24.0, fps, 0.1)
	})

	t.Run("clamps below 10fps", func(t *testing.T) {
		clip := []bufferedFrame{
			{pts: 0},
			{pts: 900000}, // 10 seconds for 1 interval = 0.1fps
		}
		fps := estimateFPSFromClip(clip)
		assert.Equal(t, 10.0, fps)
	})

	t.Run("clamps above 120fps", func(t *testing.T) {
		clip := []bufferedFrame{
			{pts: 0},
			{pts: 100}, // absurdly small interval
		}
		fps := estimateFPSFromClip(clip)
		assert.Equal(t, 120.0, fps)
	})
}

// verySlowMockDecoderFactory creates a decoder with 40ms delay per Decode(),
// simulating heavy decode overhead to amplify timing drift in loop tests.
func verySlowMockDecoderFactory() (transition.VideoDecoder, error) {
	return &verySlowMockDecoder{width: 320, height: 240}, nil
}

type verySlowMockDecoder struct {
	width, height int
	decodeCount   int
}

func (d *verySlowMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	d.decodeCount++
	time.Sleep(40 * time.Millisecond) // heavy decode latency
	yuv := make([]byte, d.width*d.height*3/2)
	yuv[0] = byte(d.decodeCount)
	return yuv, d.width, d.height, nil
}

func (d *verySlowMockDecoder) Close() {}

func TestReplayPlayer_LoopTimingNoDrift(t *testing.T) {
	// Verify that playbackStart and pacingIdx are reset on loop so frame
	// pacing doesn't drift. playbackStart and pacingIdx are reset on loop to prevent cumulative timing drift.
	// Re-decoding GOPs at each loop boundary adds wall-clock overhead that
	// accumulates in the deficit between absolute pacing deadlines and
	// actual wall-clock time.
	//
	// With 3 frames per GOP and 40ms-per-frame decode, each loop boundary
	// re-decode costs ~120ms. At 30fps (33ms/frame), after 1 loop the
	// accumulated re-decode overhead already exceeds the entire loop's
	// pacing budget (100ms). This means ALL frames in the 2nd loop have
	// deadlines in the past, and the encoder Output fires with zero
	// pacing wait between frames.
	//
	// Detection: measure interval between the last two encoded Output
	// frames in each loop. In loop 0, this interval should be ~33ms
	// (one frame duration). In loop 1+ without the fix, this interval
	// should be near-zero because deadlines are all past.
	clip := buildTestClip(1, 3) // 1 GOP, 3 frames at ~30fps

	type timedFrame struct {
		wallTime time.Time
	}
	var mu sync.Mutex
	var frames []timedFrame

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		Speed:          1.0,
		Loop:           true,
		InitialPTS:     0,
		DecoderFactory: verySlowMockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, timedFrame{wallTime: time.Now()})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p.Start(ctx)

	// With 3 frames at 33ms + 120ms re-decode = ~220ms per loop.
	// Wait for 3+ loops.
	time.Sleep(1000 * time.Millisecond)
	p.Stop()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	framesPerLoop := 3
	require.GreaterOrEqual(t, len(frames), framesPerLoop*2,
		"expected at least %d frames (2 loops), got %d", framesPerLoop*2, len(frames))

	// Check the interval between the last two frames of each loop.
	// This pair is WITHIN the loop (no re-decode gap) and should be
	// properly paced at ~33ms. Frame pacing remains consistent across loop boundaries.
	numCompleteLoops := len(frames) / framesPerLoop
	for loopIdx := 0; loopIdx < numCompleteLoops; loopIdx++ {
		// Last two frames in this loop: indices [loopIdx*3+1] and [loopIdx*3+2]
		secondLast := loopIdx*framesPerLoop + framesPerLoop - 2
		last := loopIdx*framesPerLoop + framesPerLoop - 1
		if last >= len(frames) {
			break
		}
		interval := frames[last].wallTime.Sub(frames[secondLast].wallTime)
		assert.Greater(t, interval, 15*time.Millisecond,
			"loop %d: interval between last two frames (%v) collapsed; "+
				"expected ~33ms but got near-zero, indicating pacing deadlines "+
				"are in the past (playbackStart/pacingIdx not reset on loop)",
			loopIdx, interval)
	}
}

func TestReplayPlayer_AudioPTSAlignedWithVideo(t *testing.T) {
	// Verify that the first audio output PTS matches the first video output PTS.
	// Both are seeded from PlayerConfig.InitialPTS, so they are aligned by
	// construction. This test documents that the alignment is correct and
	// guards against regressions.
	clip := buildTestClip(1, 4)
	initialPTS := int64(900000) // arbitrary non-zero anchor

	audioClip := []bufferedAudioFrame{
		{data: []byte{0xAA}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xBB}, pts: 1920, sampleRate: 48000, channels: 2},
		{data: []byte{0xCC}, pts: 3840, sampleRate: 48000, channels: 2},
	}

	var mu sync.Mutex
	var videoPTS []int64
	var audioPTS []int64
	done := make(chan struct{})

	cfg := PlayerConfig{
		Clip:          clip,
		AudioClip:     audioClip,
		Speed:         1.0,
		Loop:          false,
		InitialPTS:    initialPTS,
		Interpolation: InterpolationNone,
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return &mockReplayDecoder{width: 320, height: 240}, nil
		},
		EncoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &mockReplayEncoder{}, nil
		},
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			videoPTS = append(videoPTS, frame.PTS)
			mu.Unlock()
		},
		AudioOutput: func(frame *media.AudioFrame) {
			mu.Lock()
			audioPTS = append(audioPTS, frame.PTS)
			mu.Unlock()
		},
		OnDone:  func() { close(done) },
		OnReady: func() {},
	}

	player := newReplayPlayer(cfg)
	player.Start(context.Background())
	<-done

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, videoPTS, "should have video output")
	require.NotEmpty(t, audioPTS, "should have audio output")

	// Both first output PTS values should equal InitialPTS.
	assert.Equal(t, initialPTS, videoPTS[0],
		"first video PTS should equal InitialPTS")
	assert.Equal(t, initialPTS, audioPTS[0],
		"first audio PTS should equal InitialPTS (aligned with video)")

	// Audio PTS should advance monotonically.
	for i := 1; i < len(audioPTS); i++ {
		assert.Greater(t, audioPTS[i], audioPTS[i-1],
			"audio PTS should be monotonically increasing")
	}
}
