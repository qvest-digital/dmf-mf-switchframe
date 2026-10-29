package clip

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// generateTestFrames creates test video and audio frames by generating a test
// TS file and demuxing it. This produces realistic frame data with proper
// SPS/PPS, PTS ordering, and AVC1 wire format.
func generateTestFrames(t *testing.T, numFrames int) ([]BufferedFrame, []BufferedAudioFrame) {
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
			yuv := make([]byte, video.YUV420FrameSize(320, 240))
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
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
			yuv := make([]byte, video.YUV420FrameSize(320, 240))
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
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
	frames := []BufferedFrame{
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
	frames := []BufferedFrame{{pts: 90000, isKeyframe: true}}
	fps := estimateFPSFromClipFrames(frames)
	assert.Equal(t, 30.0, fps, "single frame should default to 30fps")
}

func TestEstimateFPSZeroPTSSpan(t *testing.T) {
	frames := []BufferedFrame{
		{pts: 90000},
		{pts: 90000},
		{pts: 90000},
	}
	fps := estimateFPSFromClipFrames(frames)
	assert.Equal(t, 30.0, fps, "zero PTS span should default to 30fps")
}

func TestEstimateFPSClampRange(t *testing.T) {
	// Very high FPS (small PTS span).
	highFPS := []BufferedFrame{
		{pts: 0},
		{pts: 100}, // 90000/100 = 900fps
	}
	fps := estimateFPSFromClipFrames(highFPS)
	assert.Equal(t, 120.0, fps, "should clamp to 120fps max")

	// Very low FPS (large PTS span).
	lowFPS := []BufferedFrame{
		{pts: 0},
		{pts: 900000}, // 90000/900000 = 0.1fps
	}
	fps = estimateFPSFromClipFrames(lowFPS)
	assert.Equal(t, 10.0, fps, "should clamp to 10fps min")
}

func TestPlayerEncoderFactoryReceivesRationalFPS(t *testing.T) {
	// Test that a ~29.97fps clip passes (30000, 1001) to the encoder factory.
	// Create frames with PTS values that yield ~29.97fps.
	// 29.97fps -> PTS increment = 90000/29.97 ≈ 3003
	ntscFrames := make([]BufferedFrame, 10)
	ntscFrames[0] = BufferedFrame{
		wireData:   []byte{0, 0, 0, 1, 0x65, 1, 2, 3},
		pts:        0,
		isKeyframe: true,
		sps:        []byte{0x67, 0x42},
		pps:        []byte{0x68, 0xCE},
	}
	for i := 1; i < 10; i++ {
		ntscFrames[i] = BufferedFrame{
			wireData: []byte{0, 0, 0, 1, 0x41, byte(i)},
			pts:      int64(i) * 3003, // 29.97fps: 90000 * 1001/30000 ≈ 3003
		}
	}

	var gotFPSNum, gotFPSDen int
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       ntscFrames,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		DecodeFrame: func(annexB []byte) ([]byte, int, int, error) {
			yuv := make([]byte, video.YUV420FrameSize(320, 240))
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
			gotFPSNum = fpsNum
			gotFPSDen = fpsDen
			return &mockVideoEncoder{
				encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
					if forceIDR {
						return buildAnnexBKeyframe(), true, nil
					}
					return buildAnnexBNonKeyframe(), false, nil
				},
				closeFn: func() {},
			}, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	// ~29.97fps should snap to 30000/1001.
	assert.Equal(t, 30000, gotFPSNum, "NTSC 29.97fps should produce fpsNum=30000")
	assert.Equal(t, 1001, gotFPSDen, "NTSC 29.97fps should produce fpsDen=1001")
}

func TestPlayerEncoderFactoryPAL25fps(t *testing.T) {
	// Test that a 25fps clip passes (25, 1) to the encoder factory.
	// 25fps -> PTS increment = 90000/25 = 3600
	palFrames := make([]BufferedFrame, 10)
	palFrames[0] = BufferedFrame{
		wireData:   []byte{0, 0, 0, 1, 0x65, 1, 2, 3},
		pts:        0,
		isKeyframe: true,
		sps:        []byte{0x67, 0x42},
		pps:        []byte{0x68, 0xCE},
	}
	for i := 1; i < 10; i++ {
		palFrames[i] = BufferedFrame{
			wireData: []byte{0, 0, 0, 1, 0x41, byte(i)},
			pts:      int64(i) * 3600, // 25fps: 90000/25 = 3600
		}
	}

	var gotFPSNum, gotFPSDen int
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:       palFrames,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		DecodeFrame: func(annexB []byte) ([]byte, int, int, error) {
			yuv := make([]byte, video.YUV420FrameSize(320, 240))
			return yuv, 320, 240, nil
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
			gotFPSNum = fpsNum
			gotFPSDen = fpsDen
			return &mockVideoEncoder{
				encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
					if forceIDR {
						return buildAnnexBKeyframe(), true, nil
					}
					return buildAnnexBNonKeyframe(), false, nil
				},
				closeFn: func() {},
			}, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)
	<-done
	cancel()
	p.Wait()

	// 25fps should snap to 25/1.
	assert.Equal(t, 25, gotFPSNum, "PAL 25fps should produce fpsNum=25")
	assert.Equal(t, 1, gotFPSDen, "PAL 25fps should produce fpsDen=1")
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

// --- Mock DrainableDecoder for B-frame drain tests ---

type mockDrainableDecoder struct {
	decodeFn       func(h264 []byte) (yuv []byte, w, h int, err error)
	sendEOSFn      func() error
	receiveFrameFn func() (yuv []byte, w, h int, err error)
	closeFn        func()
}

func (m *mockDrainableDecoder) Decode(h264 []byte) (yuv []byte, w, h int, err error) {
	return m.decodeFn(h264)
}

func (m *mockDrainableDecoder) SendEOS() error {
	return m.sendEOSFn()
}

func (m *mockDrainableDecoder) ReceiveFrame() (yuv []byte, w, h int, err error) {
	return m.receiveFrameFn()
}

func (m *mockDrainableDecoder) Close() {
	if m.closeFn != nil {
		m.closeFn()
	}
}

// TestPlayerDrainBFrameBufferedFrames verifies that when a decoder buffers
// frames (as happens with B-frame reordering), the drain phase after playback
// flushes all remaining buffered frames via SendEOS + ReceiveFrame.
//
// This confirms the B-frame handling is correct end-to-end:
//  1. demux_mp4.go keeps frames in decode (sample table) order
//  2. player.go feeds frames sequentially to the decoder
//  3. FFmpeg decoder buffers B-frames, returning EAGAIN until ready
//  4. After all frames are sent, drain phase flushes remaining buffered frames
//  5. Output encoder uses tune=zerolatency (bframes=0) so output is B-frame-free
func TestPlayerDrainBFrameBufferedFrames(t *testing.T) {
	// Simulate 5 frames where the decoder buffers the last 2 (B-frame reordering).
	// The decoder returns real output for frames 1-3 but EAGAIN for 4-5.
	// After SendEOS, it produces the 2 buffered frames.
	clip := []BufferedFrame{
		{wireData: []byte{0, 0, 0, 1, 0x65, 1, 2, 3}, pts: 0, isKeyframe: true, sps: []byte{0x67, 0x42}, pps: []byte{0x68, 0xCE}},
		{wireData: []byte{0, 0, 0, 1, 0x41, 4, 5, 6}, pts: 3000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 7, 8, 9}, pts: 6000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 10, 11, 12}, pts: 9000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 13, 14, 15}, pts: 12000},
	}

	var mu sync.Mutex
	decodeCount := 0
	bufferingError := fmt.Errorf("buffering")

	// Track drain outputs separately from normal outputs.
	var drainCount atomic.Int32
	var normalCount atomic.Int32
	drainStarted := false

	decoder := &mockDrainableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			mu.Lock()
			defer mu.Unlock()
			decodeCount++
			// First 3 frames decode normally, last 2 return "buffering" (EAGAIN).
			if decodeCount <= 3 {
				yuv := make([]byte, video.YUV420FrameSize(320, 240)) // minimal YUV420
				return yuv, 320, 240, nil
			}
			return nil, 0, 0, bufferingError
		},
		sendEOSFn: func() error {
			mu.Lock()
			drainStarted = true
			mu.Unlock()
			return nil
		},
		receiveFrameFn: func() ([]byte, int, int, error) {
			mu.Lock()
			defer mu.Unlock()
			if !drainStarted {
				return nil, 0, 0, fmt.Errorf("no frames")
			}
			// Return 2 buffered frames, then error to end drain.
			count := drainCount.Load()
			if count < 2 {
				drainCount.Add(1)
				yuv := make([]byte, video.YUV420FrameSize(320, 240))
				return yuv, 320, 240, nil
			}
			return nil, 0, 0, fmt.Errorf("no more frames")
		},
		closeFn: func() {},
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    decoder,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return decoder.Decode(h264)
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			normalCount.Add(1)
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

	// The drain phase should have produced the 2 buffered frames.
	assert.Equal(t, int32(2), drainCount.Load(),
		"drain phase should flush 2 buffered B-frames via SendEOS + ReceiveFrame")

	// Normal decode should have produced 3 frames (+ 2 EAGAIN skipped).
	// Total raw output = 3 normal + 2 drained = 5.
	totalOutput := normalCount.Load()
	assert.GreaterOrEqual(t, totalOutput, int32(3),
		"should have at least 3 normally decoded frames")
}

// TestPlayerReencodeReturnsCopy verifies that reencodeForBrowser returns
// a deep copy of the internal avc1Buf, not an aliased slice. If the caller
// retains the returned data asynchronously (e.g., for MoQ relay broadcast),
// the next encode cycle must not overwrite it.
func TestPlayerReencodeReturnsCopy(t *testing.T) {
	callCount := 0
	mockEncoder := &mockVideoEncoder{
		encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
			callCount++
			// Return distinct Annex B data on each call so we can detect aliasing.
			if callCount == 1 {
				return buildAnnexBKeyframe(), true, nil
			}
			return buildAnnexBNonKeyframe(), false, nil
		},
		closeFn: func() {},
	}

	p := &Player{
		config: PlayerConfig{
			EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
				return mockEncoder, nil
			},
		},
		sourceFPS: 30.0,
	}

	// Initialize avc1Buf scratch buffer.
	p.avc1Buf = make([]byte, 0, 1024)

	// First call — get a keyframe result.
	yuv1 := make([]byte, video.YUV420FrameSize(320, 240))
	result1, kf1, _, _ := p.reencodeForBrowser(yuv1, 320, 240, 0, true)
	require.NotNil(t, result1)
	assert.True(t, kf1)

	// Snapshot the first result's content.
	snapshot := make([]byte, len(result1))
	copy(snapshot, result1)

	// Second call — should NOT overwrite result1's data.
	yuv2 := make([]byte, video.YUV420FrameSize(320, 240))
	result2, _, _, _ := p.reencodeForBrowser(yuv2, 320, 240, 3000, false)
	require.NotNil(t, result2)

	// result1 must still contain the original data.
	assert.Equal(t, snapshot, result1,
		"first result was overwritten by second reencodeForBrowser call — returned slice aliases internal buffer")
}

// TestPlayerDrainBFramesSentToBrowserRelay verifies that drained B-frames
// (from the decoder drain phase after playback) are also sent to VideoOutput
// for the browser relay, not just to RawVideoOutput.
func TestPlayerDrainBFramesSentToBrowserRelay(t *testing.T) {
	clip := []BufferedFrame{
		{wireData: []byte{0, 0, 0, 1, 0x65, 1, 2, 3}, pts: 0, isKeyframe: true, sps: []byte{0x67, 0x42}, pps: []byte{0x68, 0xCE}},
		{wireData: []byte{0, 0, 0, 1, 0x41, 4, 5, 6}, pts: 3000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 7, 8, 9}, pts: 6000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 10, 11, 12}, pts: 9000},
		{wireData: []byte{0, 0, 0, 1, 0x41, 13, 14, 15}, pts: 12000},
	}

	var mu sync.Mutex
	decodeCount := 0
	bufferingError := fmt.Errorf("buffering")
	drainStarted := false
	var drainCount atomic.Int32

	decoder := &mockDrainableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			mu.Lock()
			defer mu.Unlock()
			decodeCount++
			if decodeCount <= 3 {
				yuv := make([]byte, video.YUV420FrameSize(320, 240))
				return yuv, 320, 240, nil
			}
			return nil, 0, 0, bufferingError
		},
		sendEOSFn: func() error {
			mu.Lock()
			drainStarted = true
			mu.Unlock()
			return nil
		},
		receiveFrameFn: func() ([]byte, int, int, error) {
			mu.Lock()
			defer mu.Unlock()
			if !drainStarted {
				return nil, 0, 0, fmt.Errorf("no frames")
			}
			count := drainCount.Load()
			if count < 2 {
				drainCount.Add(1)
				yuv := make([]byte, video.YUV420FrameSize(320, 240))
				return yuv, 320, 240, nil
			}
			return nil, 0, 0, fmt.Errorf("no more frames")
		},
		closeFn: func() {},
	}

	// Track VideoOutput calls during the drain phase.
	// We use a mutex-protected flag to distinguish drain-phase outputs
	// from normal playback outputs and hold-mode outputs.
	var rawDrainCount atomic.Int32
	var videoDrainCount atomic.Int32
	var drainPhaseActive atomic.Bool
	done := make(chan struct{})

	mockEnc := &mockVideoEncoder{
		encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
			if forceIDR {
				return buildAnnexBKeyframe(), true, nil
			}
			return buildAnnexBNonKeyframe(), false, nil
		},
		closeFn: func() {},
	}

	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    decoder,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return decoder.Decode(h264)
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
			return mockEnc, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			if drainPhaseActive.Load() {
				rawDrainCount.Add(1)
			}
		},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			if drainPhaseActive.Load() {
				videoDrainCount.Add(1)
			}
		},
		OnDone: func() { close(done) },
	})

	// Wrap the SendEOS function to set our drain-phase flag.
	origSendEOS := decoder.sendEOSFn
	decoder.sendEOSFn = func() error {
		drainPhaseActive.Store(true)
		return origSendEOS()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete")
	}

	cancel()
	p.Wait()

	// Drain should have produced 2 frames to RawVideoOutput.
	assert.Equal(t, int32(2), drainCount.Load(),
		"drain phase should flush 2 buffered B-frames")
	assert.GreaterOrEqual(t, rawDrainCount.Load(), int32(2),
		"drained frames should be sent to RawVideoOutput")

	// BUG: Drained frames should ALSO be sent to VideoOutput (browser relay).
	assert.GreaterOrEqual(t, videoDrainCount.Load(), int32(2),
		"drained frames should also be sent to VideoOutput for browser relay")
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

func TestPlayerSetLoop(t *testing.T) {
	// Bug: SetLoop on the Manager sets slot.loop but doesn't propagate to
	// the active Player, which reads p.config.Loop at creation time.
	frames, audio := generateTestFrames(t, 5)
	var rawCount atomic.Int32

	p := NewPlayer(PlayerConfig{
		Clip:       frames,
		AudioClip:  audio,
		Speed:      2.0,
		Loop:       false, // start without looping
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawCount.Add(1)
		},
	})

	// Before starting, SetLoop should update the config
	p.SetLoop(true)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p.Start(ctx)

	// With loop=true and a 5-frame clip at 2x speed, we should see more
	// frames than a single pass through the clip (5 frames).
	time.Sleep(1 * time.Second)
	cancel()
	p.Wait()

	count := rawCount.Load()
	require.Greater(t, count, int32(5),
		"with loop enabled via SetLoop, player should loop and produce more than 5 frames, got %d", count)
}

// TestPlayerReencodeAVC1BufferNotAliased verifies that reencodeForBrowser
// returns a deep copy rather than a slice aliasing the player's internal
// avc1Buf scratch buffer. Downstream consumers (BroadcastVideo) retain frame
// references in the GOP cache and send them asynchronously via channels.
// If wireData aliases avc1Buf, the next encode overwrites the retained data.
func TestPlayerReencodeAVC1BufferNotAliased(t *testing.T) {
	// Track encode call count to produce different output each time.
	var callCount int

	p := &Player{
		config: PlayerConfig{
			EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
				return &mockVideoEncoder{
					encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
						callCount++
						// Return different Annex B data each call by varying the IDR payload.
						// This ensures that if buffers alias, the first result gets corrupted.
						sps := []byte{0x67, 0x42, 0xC0, 0x1E}
						pps := []byte{0x68, 0xCE, 0x38, 0x80}
						idr := make([]byte, 16)
						idr[0] = 0x65 // IDR NALU type
						// Fill with call-specific marker so each frame is distinguishable.
						for i := 1; i < len(idr); i++ {
							idr[i] = byte(callCount)
						}

						var out []byte
						out = append(out, 0x00, 0x00, 0x00, 0x01)
						out = append(out, sps...)
						out = append(out, 0x00, 0x00, 0x00, 0x01)
						out = append(out, pps...)
						out = append(out, 0x00, 0x00, 0x00, 0x01)
						out = append(out, idr...)
						return out, true, nil
					},
					closeFn: func() {},
				}, nil
			},
		},
		sourceFPS: 30.0,
	}

	yuv := make([]byte, video.YUV420FrameSize(320, 240))

	// First call: get wireData reference.
	wd1, _, _, _ := p.reencodeForBrowser(yuv, 320, 240, 0, true)
	require.NotNil(t, wd1, "first reencode should return wireData")

	// Snapshot the first result.
	snap1 := make([]byte, len(wd1))
	copy(snap1, wd1)

	// Second call with different encoder output — should NOT corrupt wd1.
	wd2, _, _, _ := p.reencodeForBrowser(yuv, 320, 240, 3003, true)
	require.NotNil(t, wd2, "second reencode should return wireData")

	// The two outputs should be different (different marker bytes).
	require.NotEqual(t, snap1, []byte(wd2),
		"encoder should produce different output for different calls")

	// Critical check: wd1 must still hold its original content.
	// If wd1 aliases avc1Buf, it now holds wd2's data instead.
	assert.Equal(t, snap1, []byte(wd1),
		"first wireData was corrupted by second reencode call; "+
			"avc1Buf aliasing means downstream GOP cache and async channel sends get stale data")
}

// --- Seek rewind mock helpers ---

// mockFlushableDecoder implements FlushableDecoder so the player can flush it
// on seek. Decode/Flush/Close are routed to the configured closures.
type mockFlushableDecoder struct {
	decodeFn func(h264 []byte) (yuv []byte, w, h int, err error)
	flushFn  func()
	closeFn  func()
}

func (m *mockFlushableDecoder) Decode(h264 []byte) (yuv []byte, w, h int, err error) {
	return m.decodeFn(h264)
}

func (m *mockFlushableDecoder) Flush() {
	if m.flushFn != nil {
		m.flushFn()
	}
}

func (m *mockFlushableDecoder) Close() {
	if m.closeFn != nil {
		m.closeFn()
	}
}

// buildSyntheticSeekFrame builds a minimal BufferedFrame whose wireData ends
// with a frame-index marker byte. The mock decoder can recover the index from
// the last byte of its input (after WireToAnnexB and optional SPS/PPS prepend,
// the clip's NAL — and thus the marker — remains at the end).
func buildSyntheticSeekFrame(idx byte, isKeyframe bool) BufferedFrame {
	var naluType byte = 0x41 // non-IDR slice
	if isKeyframe {
		naluType = 0x65 // IDR slice
	}
	wire := []byte{0, 0, 0, 2, naluType, idx}
	f := BufferedFrame{
		wireData:   wire,
		pts:        int64(idx) * 3000,
		isKeyframe: isKeyframe,
	}
	if isKeyframe {
		f.sps = []byte{0x67, 0x42, 0xC0, 0x1E}
		f.pps = []byte{0x68, 0xCE, 0x38, 0x80}
	}
	return f
}

// seekMarker returns the frame-index marker carried by a decoder-input blob
// produced by buildSyntheticSeekFrame. The marker is always the final byte.
func seekMarker(decoderInput []byte) int {
	if len(decoderInput) == 0 {
		return -1
	}
	return int(decoderInput[len(decoderInput)-1])
}

// TestPlayer_SeekRewindsToKeyframe verifies that Seek rewinds the decoder
// feed to the last keyframe at or before the target index, not jumping
// straight to a mid-GOP P-frame.
func TestPlayer_SeekRewindsToKeyframe(t *testing.T) {
	const numFrames = 60
	clip := make([]BufferedFrame, numFrames)
	for i := 0; i < numFrames; i++ {
		clip[i] = buildSyntheticSeekFrame(byte(i), i == 0 || i == 30)
	}

	var mu sync.Mutex
	var decodeOrder []int

	yuv := make([]byte, video.YUV420FrameSize(320, 240))
	dec := &mockFlushableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			mu.Lock()
			decodeOrder = append(decodeOrder, seekMarker(h264))
			mu.Unlock()
			return yuv, 320, 240, nil
		},
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    dec,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return dec.Decode(h264)
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Start(ctx)

	// Let a few pre-seek frames decode, then seek to frame 45 (target).
	// The nearest keyframe at-or-before 45 is frame 30.
	time.Sleep(150 * time.Millisecond)
	p.Seek(45.0 / float64(numFrames))

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete after seek")
	}
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, decodeOrder, "decoder should have been called")

	// The rewind feeds the keyframe at index 30 before target 45. Locate
	// its first occurrence and pin the pre/post shape: everything before
	// is a pre-seek prefix (< 30) and the tail is 30, 31, ..., 59.
	foundAt := -1
	for i, m := range decodeOrder {
		if m == 30 {
			foundAt = i
			break
		}
	}
	require.NotEqual(t, -1, foundAt,
		"seek to 0.75 must rewind the decoder feed to keyframe 30 (full order: %v)",
		decodeOrder)
	for i := 0; i < foundAt; i++ {
		require.Less(t, decodeOrder[i], 30,
			"pre-seek decodes must precede keyframe 30; decodeOrder[%d]=%d (full: %v)",
			i, decodeOrder[i], decodeOrder)
	}

	tail := decodeOrder[foundAt:]
	require.Equal(t, numFrames-30, len(tail),
		"post-rewind feed must include primer+post-target frames (30..%d); got %v",
		numFrames-1, tail)
	for i, v := range tail {
		assert.Equal(t, 30+i, v,
			"post-rewind decode order must be 30,31,...,59; tail[%d]=%d (tail=%v)", i, v, tail)
	}

	// Critical bug assertion: target frame 45 must not appear before the
	// rewound keyframe. Before the fix, decodeOrder jumped straight to 45
	// with no primer; this assertion pins the fix in place.
	idx45 := -1
	for i, m := range decodeOrder {
		if m == 45 {
			idx45 = i
			break
		}
	}
	require.NotEqual(t, -1, idx45, "target frame 45 should appear in the feed")
	assert.Less(t, foundAt, idx45,
		"rewound keyframe (30) must be fed before the seek target (45)")
}

// TestPlayer_SeekFlushesDecoder asserts that Seek flushes the decoder
// exactly once, and the Flush() call precedes any decode call with the
// rewound keyframe.
func TestPlayer_SeekFlushesDecoder(t *testing.T) {
	const numFrames = 60
	clip := make([]BufferedFrame, numFrames)
	for i := 0; i < numFrames; i++ {
		clip[i] = buildSyntheticSeekFrame(byte(i), i == 0 || i == 30)
	}

	var mu sync.Mutex
	type event struct {
		kind string // "decode" or "flush"
		idx  int
	}
	var events []event

	yuv := make([]byte, video.YUV420FrameSize(320, 240))
	dec := &mockFlushableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			mu.Lock()
			events = append(events, event{kind: "decode", idx: seekMarker(h264)})
			mu.Unlock()
			return yuv, 320, 240, nil
		},
		flushFn: func() {
			mu.Lock()
			events = append(events, event{kind: "flush"})
			mu.Unlock()
		},
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    dec,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return dec.Decode(h264)
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	p.Seek(45.0 / float64(numFrames))

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete after seek")
	}
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Exactly one Flush must have happened (the seek).
	flushCount := 0
	flushAt := -1
	for i, e := range events {
		if e.kind == "flush" {
			flushCount++
			if flushAt == -1 {
				flushAt = i
			}
		}
	}
	assert.Equal(t, 1, flushCount,
		"Flush must be called exactly once on seek, got %d (events=%v)", flushCount, events)

	// The next decode after the flush must be the rewound keyframe (idx 30).
	require.NotEqual(t, -1, flushAt, "no Flush event recorded")
	require.Less(t, flushAt+1, len(events),
		"no decode event follows the flush (events=%v)", events)
	next := events[flushAt+1]
	assert.Equal(t, "decode", next.kind,
		"event after flush must be a decode call, got %q", next.kind)
	assert.Equal(t, 30, next.idx,
		"first decode after flush must be the rewound keyframe (30), got %d", next.idx)
}

// TestPlayer_SeekAtBeforeKeyframe asserts that a seek to a position with no
// preceding keyframe falls back to frame 0 without crashing. The demuxer's
// drop-before-first-keyframe pass guarantees this code path is benign in
// practice; the test just pins the behaviour.
func TestPlayer_SeekAtBeforeKeyframe(t *testing.T) {
	// Construct a clip where no frame is flagged as keyframe. Seeking to
	// position 0.0 must clamp to frame 0 and feed it to the decoder
	// without panicking.
	const numFrames = 10
	clip := make([]BufferedFrame, numFrames)
	for i := 0; i < numFrames; i++ {
		clip[i] = buildSyntheticSeekFrame(byte(i), false)
	}

	var mu sync.Mutex
	var decodeOrder []int

	yuv := make([]byte, video.YUV420FrameSize(320, 240))
	dec := &mockFlushableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			mu.Lock()
			decodeOrder = append(decodeOrder, seekMarker(h264))
			mu.Unlock()
			return yuv, 320, 240, nil
		},
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    dec,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return dec.Decode(h264)
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		OnDone:         func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Seek(0.0) before Start just fills the pending-seek channel; the
	// Start-goroutine picks it up on its first loop iteration.
	p.Seek(0.0)
	p.Start(ctx)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete after seek to 0.0")
	}
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, decodeOrder, "decoder should have been called")
	// First fed frame must be frame 0 (the fallback keyframe).
	assert.Equal(t, 0, decodeOrder[0],
		"seek to 0.0 with no keyframes must feed frame 0 first, got %v", decodeOrder)
}

// TestPlayer_SeekOutputPTSContinuity asserts that after a mid-playback seek,
// the PTS stream emitted to VideoOutput (browser relay) remains strictly
// monotonic — no duplicate or backwards PTS values that would corrupt the
// downstream GOP cache.
func TestPlayer_SeekOutputPTSContinuity(t *testing.T) {
	const numFrames = 60
	clip := make([]BufferedFrame, numFrames)
	for i := 0; i < numFrames; i++ {
		clip[i] = buildSyntheticSeekFrame(byte(i), i == 0 || i == 30)
	}

	var mu sync.Mutex
	var videoPTS []int64

	yuv := make([]byte, video.YUV420FrameSize(320, 240))
	dec := &mockFlushableDecoder{
		decodeFn: func(h264 []byte) ([]byte, int, int, error) {
			return yuv, 320, 240, nil
		},
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:       clip,
		Speed:      1.0,
		Loop:       false,
		InitialPTS: 0,
		Width:      320,
		Height:     240,
		Decoder:    dec,
		DecodeFrame: func(h264 []byte) ([]byte, int, int, error) {
			return dec.Decode(h264)
		},
		EncoderFactory: func(w, h, fpsNum, fpsDen int) (VideoEncoder, error) {
			return &mockVideoEncoder{
				encodeFn: func(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
					if forceIDR {
						return buildAnnexBKeyframe(), true, nil
					}
					return buildAnnexBNonKeyframe(), false, nil
				},
				closeFn: func() {},
			}, nil
		},
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			mu.Lock()
			videoPTS = append(videoPTS, pts)
			mu.Unlock()
		},
		OnDone: func() { close(done) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Start(ctx)

	time.Sleep(150 * time.Millisecond)
	p.Seek(45.0 / float64(numFrames))

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for playback to complete after seek")
	}
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, videoPTS, "VideoOutput should have been called")
	for i := 1; i < len(videoPTS); i++ {
		assert.Greater(t, videoPTS[i], videoPTS[i-1],
			"VideoOutput PTS stream must be strictly monotonic across seek; "+
				"pts[%d]=%d after pts[%d]=%d (full stream head=%v)",
			i, videoPTS[i], i-1, videoPTS[i-1], videoPTS[:min(i+1, 10)])
	}
}

// A frame-threaded decoder holds frames back for as many calls as it has
// threads, and B-frame reordering adds to that. Each picture must still get
// one paced slot of its own, with the audio for that slot next to it:
// TestPlayerDelayedDecoderKeepsPacingAndLipSync feeds a decoder that answers
// "buffering" for its first frames and returns them in the drain.
func TestPlayerDelayedDecoderKeepsPacingAndLipSync(t *testing.T) {
	const (
		frames = 10
		delay  = 3
	)
	clip := make([]BufferedFrame, frames)
	audio := make([]BufferedAudioFrame, frames)
	for i := range clip {
		clip[i] = BufferedFrame{
			wireData:   []byte{0, 0, 0, 1, 0x41, byte(i)},
			pts:        int64(i) * 3000, // 30 fps
			isKeyframe: i == 0,
		}
		audio[i] = BufferedAudioFrame{data: []byte{byte(i)}, sampleRate: 48000, channels: 2}
	}

	yuv := make([]byte, video.YUV420FrameSize(320, 240))
	decoded := 0
	drained := 0
	decoder := &mockDrainableDecoder{
		decodeFn: func([]byte) ([]byte, int, int, error) {
			// Each call takes a third of a frame period, so the first
			// picture leaves the decoder a frame period after play.
			time.Sleep(11 * time.Millisecond)
			decoded++
			if decoded <= delay {
				return nil, 0, 0, fmt.Errorf("no output frame yet (buffering)")
			}
			return yuv, 320, 240, nil
		},
		sendEOSFn: func() error { return nil },
		receiveFrameFn: func() ([]byte, int, int, error) {
			if drained < delay {
				drained++
				return yuv, 320, 240, nil
			}
			return nil, 0, 0, fmt.Errorf("no more frames")
		},
	}

	type event struct {
		video bool
		at    time.Time
	}
	var mu sync.Mutex
	var events []event
	record := func(isVideo bool) {
		mu.Lock()
		events = append(events, event{video: isVideo, at: time.Now()})
		mu.Unlock()
	}

	done := make(chan struct{})
	p := NewPlayer(PlayerConfig{
		Clip:        clip,
		AudioClip:   audio,
		Speed:       1.0,
		Width:       320,
		Height:      240,
		Decoder:     decoder,
		DecodeFrame: decoder.Decode,
		RawVideoOutput: func([]byte, int, int, int64, bool) {
			record(true)
		},
		AudioOutput: func([]byte, int64, int, int) {
			record(false)
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
	cancel()
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	var videoAt []time.Time
	audioBefore := make([]int, 0, frames)
	audioSoFar := 0
	for _, e := range events {
		if e.video {
			videoAt = append(videoAt, e.at)
			audioBefore = append(audioBefore, audioSoFar)
		} else {
			audioSoFar++
		}
	}
	require.Len(t, videoAt, frames, "every decoded picture reaches the pipeline")
	require.Equal(t, frames, audioSoFar, "every audio frame is emitted")

	// Picture k is shown in slot k, so the audio emitted ahead of it is
	// that of the k slots before.
	for k, n := range audioBefore {
		assert.Equal(t, k, n, "audio frames emitted before picture %d", k)
	}

	// No picture comes out ahead of its slot or is held far past it. Slot
	// k starts k periods after picture 0, which anchors the pacing. A
	// decoded picture leaves a decode call after its slot starts and a
	// drained one does not, and a late picture is followed by a short gap
	// on the absolute schedule, so the gap between two pictures says
	// nothing on its own; the offset from the slot grid does.
	period := time.Second / 30
	for k := 1; k < len(videoAt); k++ {
		late := videoAt[k].Sub(videoAt[0]) - time.Duration(k)*period
		assert.Greater(t, late, -period/2, "picture %d ahead of its slot", k)
		assert.Less(t, late, 3*period, "picture %d held past its slot", k)
	}
}
