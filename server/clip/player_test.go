package clip

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestFrames creates test video and audio frames by generating a test
// TS file and demuxing it. This produces realistic frame data with proper
// SPS/PPS, PTS ordering, and AVC1 wire format.
func generateTestFrames(t *testing.T, numFrames int) ([]bufferedFrame, []bufferedAudioFrame) {
	t.Helper()
	data := generateTestTS(t, numFrames)
	tmpFile := writeTemp(t, data, ".ts")
	frames, audioFrames, err := DemuxFile(tmpFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames from test TS")
	return frames, audioFrames
}

func TestPlayerPlayToCompletion(t *testing.T) {
	frames, audioFrames := generateTestFrames(t, 30)

	var rawCount atomic.Int32
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audioFrames,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete")
	}

	if rawCount.Load() == 0 {
		t.Error("expected raw video output")
	}
}

func TestPlayerHoldLastFrame(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	if p.State() != StateHolding {
		t.Errorf("State = %q, want holding", p.State())
	}
}

func TestPlayerPauseResume(t *testing.T) {
	frames, audio := generateTestFrames(t, 60)
	var rawCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	p.Pause()
	if p.State() != StatePaused {
		t.Errorf("State after pause = %q, want paused", p.State())
	}

	countAtPause := rawCount.Load()
	time.Sleep(200 * time.Millisecond)

	p.Resume()
	time.Sleep(200 * time.Millisecond)
	if rawCount.Load() <= countAtPause {
		t.Error("should resume producing frames after Resume()")
	}

	cancel()
	p.Wait()
}

func TestPlayerStop(t *testing.T) {
	frames, audio := generateTestFrames(t, 60)

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           true,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
	})

	ctx := context.Background()
	p.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	p.Stop()
	p.Wait() // should return quickly
}

func TestPlayerLoop(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	var rawCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      2.0,
		Loop:       true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(1 * time.Second)
	cancel()
	p.Wait()

	if rawCount.Load() <= int32(len(frames)) {
		t.Errorf("expected loop to produce more than %d frames, got %d", len(frames), rawCount.Load())
	}
}

func TestPlayerProgress(t *testing.T) {
	frames, audio := generateTestFrames(t, 30)
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	if p.Progress() != 0.0 {
		t.Error("initial progress should be 0")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	if p.Progress() < 0.9 {
		t.Errorf("final progress = %f, want ~1.0", p.Progress())
	}
}

func TestPlayerSpeedAboveOne(t *testing.T) {
	frames, audio := generateTestFrames(t, 30)
	done := make(chan struct{})
	start := time.Now()

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          2.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	elapsed := time.Since(start)

	if elapsed > 800*time.Millisecond {
		t.Errorf("2x playback took %v, expected <800ms", elapsed)
	}
}

func TestPlayerSeek(t *testing.T) {
	frames, audio := generateTestFrames(t, 60)
	var rawCount atomic.Int32
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	// Let it play a bit then seek near the end.
	time.Sleep(100 * time.Millisecond)
	p.Seek(0.9)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete after seek")
	}

	// Should have produced fewer frames than the full clip because we seeked.
	if rawCount.Load() == 0 {
		t.Error("expected some frames after seek")
	}
}

func TestPlayerSetSpeed(t *testing.T) {
	frames, audio := generateTestFrames(t, 30)
	var rawCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      0.5,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(200 * time.Millisecond)
	p.SetSpeed(2.0)
	time.Sleep(500 * time.Millisecond)
	cancel()
	p.Wait()

	assert.Greater(t, rawCount.Load(), int32(0), "should have produced frames")
}

func TestPlayerEmptyClip(t *testing.T) {
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:           nil,
		AudioClip:      nil,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Start(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout: empty clip should complete immediately")
	}
}

func TestPlayerDoubleStop(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           true,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
	})

	ctx := context.Background()
	p.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Double stop should not panic.
	p.Stop()
	p.Stop()
	p.Wait()
}

func TestPlayerPauseBeforeStart(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
	})

	// Pause before start should not panic.
	p.Pause()
	assert.Equal(t, StateLoaded, p.State())
}

func TestPlayerResumeWithoutPause(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Start(ctx)

	// Resume without pause should not panic and should keep playing.
	p.Resume()
	time.Sleep(100 * time.Millisecond)
	cancel()
	p.Wait()
}

func TestPlayerMonotonicPTS(t *testing.T) {
	frames, audio := generateTestFrames(t, 30)
	var lastPTS atomic.Int64
	lastPTS.Store(-1)
	var ptsViolations atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			prev := lastPTS.Swap(pts)
			if prev >= 0 && pts <= prev {
				ptsViolations.Add(1)
			}
		},
	})

	done := make(chan struct{})
	origOnDone := p.config.OnDone
	p.config.OnDone = func() {
		if origOnDone != nil {
			origOnDone()
		}
		close(done)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	assert.Equal(t, int32(0), ptsViolations.Load(), "PTS should be monotonically increasing")
}

func TestPlayerAudioOutput(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	require.NotEmpty(t, audio, "test needs audio frames")

	var audioCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		AudioOutput: func(data []byte, pts int64, sampleRate, channels int) {
			audioCount.Add(1)
		},
	})

	done := make(chan struct{})
	p.config.OnDone = func() { close(done) }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	assert.Greater(t, audioCount.Load(), int32(0), "should have output audio frames")
}

func TestPlayerHoldFrameOutputsDuringHold(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	var rawCount atomic.Int32
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done

	// After completion, the player should be in StateHolding.
	require.Equal(t, StateHolding, p.State())

	// Wait a bit and check that more frames have been produced (hold outputs at 1fps).
	// Use 2.5s to give CI (slower machines) enough margin for at least one 1fps tick.
	countAfterDone := rawCount.Load()
	time.Sleep(2500 * time.Millisecond)
	assert.Greater(t, rawCount.Load(), countAfterDone, "hold should continue outputting frames at 1fps")

	cancel()
	p.Wait()
}

func TestPlayerSlowMotion(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	var rawCount atomic.Int32
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      0.5,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Start(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for slow-motion playback")
	}

	// At 0.5x speed, we duplicate each frame so should have >= original count.
	assert.GreaterOrEqual(t, rawCount.Load(), int32(len(frames)),
		"slow-motion should produce at least as many frames as the clip")
}

func TestPlayerLoopMonotonicPTS(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	var lastPTS atomic.Int64
	lastPTS.Store(-1)
	var ptsViolations atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      2.0,
		Loop:       true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			prev := lastPTS.Swap(pts)
			if prev >= 0 && pts <= prev {
				ptsViolations.Add(1)
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(1 * time.Second)
	cancel()
	p.Wait()

	assert.Equal(t, int32(0), ptsViolations.Load(),
		"PTS should remain monotonic across loop boundaries")
}

// --- Tests for new clip player functionality ---

func TestPlayerKeyframeFlagPassthrough(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	require.True(t, frames[0].isKeyframe, "first frame should be keyframe")

	type kfRecord struct {
		idx        int
		isKeyframe bool
	}
	var records []kfRecord
	var mu sync.Mutex
	done := make(chan struct{})
	idx := 0

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			mu.Lock()
			records = append(records, kfRecord{idx: idx, isKeyframe: isKeyframe})
			idx++
			mu.Unlock()
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, records, "should have frame records")

	// First frame should be keyframe.
	assert.True(t, records[0].isKeyframe, "first output should be keyframe")

	// Count keyframes in source clip to compare.
	srcKeyframes := 0
	for _, f := range frames {
		if f.isKeyframe {
			srcKeyframes++
		}
	}

	// Count keyframes in output (excluding hold mode which always sends true).
	outputKeyframes := 0
	for _, r := range records[:len(frames)] { // only check playback frames, not hold
		if r.isKeyframe {
			outputKeyframes++
		}
	}
	assert.Equal(t, srcKeyframes, outputKeyframes,
		"output keyframe count should match source clip keyframe count")
}

func TestPlayerVideoOutputCallback(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	done := make(chan struct{})

	var videoOutputCount atomic.Int32
	var gotKeyframe atomic.Bool
	var gotSPS atomic.Bool

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			videoOutputCount.Add(1)
			if isKeyframe {
				gotKeyframe.Store(true)
			}
			if sps != nil {
				gotSPS.Store(true)
			}
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	assert.Greater(t, videoOutputCount.Load(), int32(0), "should have video output")
	assert.True(t, gotKeyframe.Load(), "should have at least one keyframe")
	assert.True(t, gotSPS.Load(), "should have SPS on keyframe")
}

func TestPlayerOnVideoInfoCallback(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	done := make(chan struct{})

	var infoCallCount atomic.Int32
	var infoW, infoH int

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnVideoInfo: func(sps, pps []byte, width, height int) {
			infoCallCount.Add(1)
			infoW = width
			infoH = height
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	assert.Equal(t, int32(1), infoCallCount.Load(), "OnVideoInfo should be called exactly once")
	assert.Greater(t, infoW, 0, "width should be positive")
	assert.Greater(t, infoH, 0, "height should be positive")
}

func TestPlayerReencodeForBrowser(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	done := make(chan struct{})

	var encodeCount atomic.Int32
	var encoderClosed atomic.Bool

	// Mock encoder that returns synthetic Annex B data.
	mockEncoder := &mockVideoEncoder{
		encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
			encodeCount.Add(1)
			// Return minimal Annex B with SPS+PPS+IDR for first, non-IDR for rest.
			if forceIDR {
				return buildAnnexBKeyframe(), true, nil
			}
			return buildAnnexBNonKeyframe(), false, nil
		},
		closeFn: func() {
			encoderClosed.Store(true)
		},
	}

	var videoOutputCount atomic.Int32
	var reEncodedKeyframes atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		DecodeFrame: func(annexB []byte) ([]byte, int, int, error) {
			// Return fake YUV data.
			yuv := make([]byte, 320*240*3/2)
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fps int) (VideoEncoder, error) {
			assert.Equal(t, 320, w)
			assert.Equal(t, 240, h)
			return mockEncoder, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			videoOutputCount.Add(1)
			if isKeyframe {
				reEncodedKeyframes.Add(1)
			}
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	assert.Greater(t, encodeCount.Load(), int32(0), "encoder should have been called")
	assert.Greater(t, videoOutputCount.Load(), int32(0), "VideoOutput should have been called")
	assert.True(t, encoderClosed.Load(), "encoder should be closed on player stop")
}

func TestPlayerReencodeFailureFallsBack(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	done := make(chan struct{})

	var videoOutputCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		DecodeFrame: func(annexB []byte) ([]byte, int, int, error) {
			yuv := make([]byte, 320*240*3/2)
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fps int) (VideoEncoder, error) {
			// Encoder always returns error — should fall back to original wire data.
			return &mockVideoEncoder{
				encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
					return nil, false, assert.AnError
				},
				closeFn: func() {},
			}, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			videoOutputCount.Add(1)
			// Should still receive original wire data (fallback path).
			assert.NotEmpty(t, wireData, "fallback should still provide wire data")
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	assert.Greater(t, videoOutputCount.Load(), int32(0),
		"VideoOutput should still be called with original wire data on encode failure")
}

func TestPlayerHoldVideoOutput(t *testing.T) {
	frames, audio := generateTestFrames(t, 5)
	done := make(chan struct{})

	// Track total video output count and count after OnDone (hold mode).
	var totalVideoCount atomic.Int32
	var doneSignaled atomic.Bool
	var holdVideoCount atomic.Int32
	var holdAllKeyframes atomic.Bool
	holdAllKeyframes.Store(true)

	p := NewPlayer(PlayerConfig{
		Clip:           frames,
		AudioClip:      audio,
		Speed:          1.0,
		Loop:           false,
		InitialPTS:     0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			totalVideoCount.Add(1)
			if doneSignaled.Load() {
				holdVideoCount.Add(1)
				if !isKeyframe {
					holdAllKeyframes.Store(false)
				}
			}
		},
		OnDone: func() {
			doneSignaled.Store(true)
			close(done)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	<-done
	require.Equal(t, StateHolding, p.State())

	// Hold loop outputs at 1fps. Wait long enough for at least 1 hold frame.
	time.Sleep(1200 * time.Millisecond)
	cancel()
	p.Wait()

	assert.Greater(t, holdVideoCount.Load(), int32(0), "hold should output video frames")
	assert.True(t, holdAllKeyframes.Load(),
		"all hold video frames should be keyframes")
}

func TestEstimateFPSFromClipFramesBFrameOrder(t *testing.T) {
	// Simulate B-frame decode order: PTS values are not monotonically ordered.
	// Decode order: I(0), P(6000), B(3000), P(9000), B(6000+3000=9000)...
	// Use a more realistic pattern:
	// I PTS=0, P PTS=9000, B PTS=3000, B PTS=6000, P PTS=12000
	frames := []bufferedFrame{
		{pts: 0, isKeyframe: true},
		{pts: 9000},
		{pts: 3000},
		{pts: 6000},
		{pts: 12000},
	}

	fps := estimateFPSFromClipFrames(frames)
	// PTS span = 12000 - 0 = 12000, intervals = 4, FPS = 4 * 90000 / 12000 = 30
	assert.InDelta(t, 30.0, fps, 1.0, "should correctly estimate FPS from B-frame ordered content")
}

func TestEstimateFPSSingleFrame(t *testing.T) {
	frames := []bufferedFrame{{pts: 90000, isKeyframe: true}}
	fps := estimateFPSFromClipFrames(frames)
	assert.Equal(t, 30.0, fps, "single frame should default to 30fps")
}

func TestEstimateFPSZeroPTSSpan(t *testing.T) {
	frames := []bufferedFrame{
		{pts: 90000},
		{pts: 90000},
		{pts: 90000},
	}
	fps := estimateFPSFromClipFrames(frames)
	assert.Equal(t, 30.0, fps, "zero PTS span should default to 30fps")
}

func TestEstimateFPSClampRange(t *testing.T) {
	// Very high FPS (small PTS span).
	highFPS := []bufferedFrame{
		{pts: 0},
		{pts: 100}, // 90000/100 = 900fps
	}
	fps := estimateFPSFromClipFrames(highFPS)
	assert.Equal(t, 120.0, fps, "should clamp to 120fps max")

	// Very low FPS (large PTS span).
	lowFPS := []bufferedFrame{
		{pts: 0},
		{pts: 900000}, // 90000/900000 = 0.1fps
	}
	fps = estimateFPSFromClipFrames(lowFPS)
	assert.Equal(t, 10.0, fps, "should clamp to 10fps min")
}

// --- Mock types for encoder tests ---

type mockVideoEncoder struct {
	encodeFn func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error)
	closeFn  func()
}

func (m *mockVideoEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return m.encodeFn(yuv, pts, forceIDR)
}

func (m *mockVideoEncoder) Close() {
	if m.closeFn != nil {
		m.closeFn()
	}
}

// buildAnnexBKeyframe returns minimal Annex B data with SPS + PPS + IDR NALU.
func buildAnnexBKeyframe() []byte {
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}
	idr := make([]byte, 32)
	idr[0] = 0x65

	var out []byte
	// SPS
	out = append(out, 0x00, 0x00, 0x00, 0x01)
	out = append(out, sps...)
	// PPS
	out = append(out, 0x00, 0x00, 0x00, 0x01)
	out = append(out, pps...)
	// IDR
	out = append(out, 0x00, 0x00, 0x00, 0x01)
	out = append(out, idr...)
	return out
}

// buildAnnexBNonKeyframe returns minimal Annex B data with a non-IDR NALU.
func buildAnnexBNonKeyframe() []byte {
	nonIDR := make([]byte, 16)
	nonIDR[0] = 0x41

	var out []byte
	out = append(out, 0x00, 0x00, 0x00, 0x01)
	out = append(out, nonIDR...)
	return out
}
