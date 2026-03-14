package clip

import (
	"context"
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
		Clip:      frames,
		AudioClip: audioFrames,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		OnDone: func() { close(done) },
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     2.0,
		Loop:      true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		OnDone: func() { close(done) },
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     2.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		OnDone: func() { close(done) },
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     0.5,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      nil,
		AudioClip: nil,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
		OnDone: func() { close(done) },
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
	})

	// Pause before start should not panic.
	p.Pause()
	assert.Equal(t, StateLoaded, p.State())
}

func TestPlayerResumeWithoutPause(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)

	p := NewPlayer(PlayerConfig{
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {},
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     1.0,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
	countAfterDone := rawCount.Load()
	time.Sleep(1200 * time.Millisecond)
	assert.Greater(t, rawCount.Load(), countAfterDone, "hold should continue outputting frames at 1fps")

	cancel()
	p.Wait()
}

func TestPlayerSlowMotion(t *testing.T) {
	frames, audio := generateTestFrames(t, 10)
	var rawCount atomic.Int32
	done := make(chan struct{})

	p := NewPlayer(PlayerConfig{
		Clip:      frames,
		AudioClip: audio,
		Speed:     0.5,
		Loop:      false,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
		Clip:      frames,
		AudioClip: audio,
		Speed:     2.0,
		Loop:      true,
		InitialPTS: 0,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
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
