package switcher

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
)

// TestSoak runs the full switching pipeline continuously for an extended duration
// to detect goroutine leaks, memory leaks, and concurrency bugs under sustained load.
//
// By default it runs for 10 seconds. Set SOAK_DURATION env var for longer runs
// (e.g., SOAK_DURATION=1h for nightly CI). Skipped with -short flag.
func TestSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test skipped in short mode")
	}

	duration := 10 * time.Second
	if d := os.Getenv("SOAK_DURATION"); d != "" {
		parsed, err := time.ParseDuration(d)
		require.NoError(t, err, "invalid SOAK_DURATION")
		duration = parsed
	}

	t.Logf("soak test running for %s", duration)

	// --- Setup ---

	const numSources = 4
	sourceKeys := make([]string, numSources)
	for i := range sourceKeys {
		sourceKeys[i] = fmt.Sprintf("cam%d", i+1)
	}

	// Program relay + capture viewer.
	programRelay := newTestRelay()
	capture := newMockProgramViewer("soak-capture")
	programRelay.AddViewer(capture)

	// Switcher.
	sw := New(programRelay)
	defer sw.Close()

	// Audio mixer wired to program relay.
	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
	})
	defer func() { _ = mixer.Close() }()

	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetMixer(mixer)

	// Register sources.
	sourceRelays := make(map[string]*testRelayWrapper)
	for _, key := range sourceKeys {
		relay := newTestRelay()
		sw.RegisterSource(key, relay)
		mixer.AddChannel(key)
		require.NoError(t, mixer.SetAFV(key, true))
		sourceRelays[key] = &testRelayWrapper{relay: relay}
	}

	// Cut to first source to start the pipeline.
	require.NoError(t, sw.Cut(context.Background(), sourceKeys[0]))

	// Send initial keyframe to clear IDR gate.
	sourceRelays[sourceKeys[0]].relay.BroadcastVideo(&media.VideoFrame{
		PTS:        0,
		IsKeyframe: true,
		WireData:   []byte{0x01},
	})

	// --- Record baseline metrics ---

	runtime.GC()
	time.Sleep(50 * time.Millisecond) // let GC settle

	var baselineMemStats runtime.MemStats
	runtime.ReadMemStats(&baselineMemStats)
	baselineGoroutines := runtime.NumGoroutine()
	t.Logf("baseline: goroutines=%d, heapAlloc=%d bytes", baselineGoroutines, baselineMemStats.HeapAlloc)

	// --- Counters ---

	var videoFramesInjected atomic.Int64
	var audioFramesInjected atomic.Int64
	var cutsPerformed atomic.Int64

	// Audio PTS monotonicity tracking.
	var audioPTSMu sync.Mutex
	var lastAudioPTS int64 = -1
	var audioPTSViolations atomic.Int64

	// --- Launch worker goroutines ---

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Frame injection: one goroutine per source @ 30fps.
	for _, key := range sourceKeys {
		wg.Add(1)
		go func(sourceKey string) {
			defer wg.Done()
			ticker := time.NewTicker(33 * time.Millisecond)
			defer ticker.Stop()

			var pts int64
			frameNum := 0
			relay := sourceRelays[sourceKey].relay

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					isKeyframe := frameNum%30 == 0 // keyframe every 30 frames (~1s)
					relay.BroadcastVideo(&media.VideoFrame{
						PTS:        pts,
						IsKeyframe: isKeyframe,
						WireData:   []byte{0x01},
					})
					videoFramesInjected.Add(1)

					relay.BroadcastAudio(&media.AudioFrame{
						PTS:        pts,
						Data:       []byte{0xAA},
						SampleRate: 48000,
						Channels:   2,
					})
					audioFramesInjected.Add(1)

					pts += 33333 // ~33ms in microseconds
					frameNum++
				}
			}
		}(key)
	}

	// Cut trigger: goroutine cuts every 2 seconds, round-robin.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		cutIndex := 1 // start at 1 since we already cut to cam1
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				target := sourceKeys[cutIndex%numSources]
				_ = sw.Cut(context.Background(), target)
				cutsPerformed.Add(1)
				cutIndex++
			}
		}
	}()

	// Monitor: goroutine samples every second to track audio PTS monotonicity
	// from the capture viewer, and logs resource usage every 10 seconds.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sampleTicker := time.NewTicker(1 * time.Second)
		logTicker := time.NewTicker(10 * time.Second)
		defer sampleTicker.Stop()
		defer logTicker.Stop()

		for {
			select {
			case <-done:
				return
			case <-sampleTicker.C:
				// Check audio PTS monotonicity from captured frames.
				capture.mu.Lock()
				audios := capture.audios
				// Drain processed audios to avoid unbounded memory growth.
				capture.audios = nil
				capture.mu.Unlock()

				audioPTSMu.Lock()
				for _, af := range audios {
					if af.PTS < lastAudioPTS {
						audioPTSViolations.Add(1)
					}
					lastAudioPTS = af.PTS
				}
				audioPTSMu.Unlock()

			case <-logTicker.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				t.Logf("monitor: goroutines=%d, heapAlloc=%d, videoIn=%d, audioIn=%d, cuts=%d",
					runtime.NumGoroutine(), ms.HeapAlloc,
					videoFramesInjected.Load(), audioFramesInjected.Load(),
					cutsPerformed.Load())
			}
		}
	}()

	// --- Run for duration ---

	time.Sleep(duration)
	close(done)
	wg.Wait()

	// --- Final metric collection ---

	// Drain any remaining audio from capture for PTS check.
	capture.mu.Lock()
	remainingAudios := capture.audios
	capture.audios = nil
	// Count total video frames received.
	videoFramesReceived := len(capture.videos)
	capture.mu.Unlock()

	audioPTSMu.Lock()
	for _, af := range remainingAudios {
		if af.PTS < lastAudioPTS {
			audioPTSViolations.Add(1)
		}
		lastAudioPTS = af.PTS
	}
	audioPTSMu.Unlock()

	// Force GC before measuring final heap.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	var finalMemStats runtime.MemStats
	runtime.ReadMemStats(&finalMemStats)
	finalGoroutines := runtime.NumGoroutine()

	t.Logf("final: goroutines=%d (baseline=%d), heapAlloc=%d (baseline=%d)",
		finalGoroutines, baselineGoroutines,
		finalMemStats.HeapAlloc, baselineMemStats.HeapAlloc)
	t.Logf("frames: videoInjected=%d, videoReceived=%d, audioInjected=%d, cuts=%d",
		videoFramesInjected.Load(), videoFramesReceived,
		audioFramesInjected.Load(), cutsPerformed.Load())

	// --- Assertions ---

	// 1. Program viewer should have received frames (within reasonable range).
	//    At 30fps with 4 sources, only program source frames pass through.
	//    With cuts every 2s and IDR gating, expect some frames dropped.
	//    Just verify we received a substantial number (at least 20% of injected per-source).
	expectedMinFrames := int(duration.Seconds()) * 30 / 5 // very conservative lower bound
	require.Greater(t, videoFramesReceived, expectedMinFrames,
		"should have received substantial number of program video frames")
	t.Logf("video frame check: received=%d, minExpected=%d", videoFramesReceived, expectedMinFrames)

	// 2. Audio PTS should be monotonic (no backwards jumps).
	require.Equal(t, int64(0), audioPTSViolations.Load(),
		"audio PTS should be monotonically non-decreasing")

	// 3. Goroutine count should not have leaked significantly.
	goroutineDelta := finalGoroutines - baselineGoroutines
	require.LessOrEqual(t, goroutineDelta, 5,
		"goroutine count should not grow by more than 5 (delta=%d, final=%d, baseline=%d)",
		goroutineDelta, finalGoroutines, baselineGoroutines)

	// 4. Heap allocation should not have grown excessively (within 2x baseline).
	//    Note: we cleared capture.audios periodically to prevent unbounded growth.
	maxHeap := baselineMemStats.HeapAlloc * 2
	if maxHeap < 50*1024*1024 { // floor of 50MB to avoid flaky failures on small heaps
		maxHeap = 50 * 1024 * 1024
	}
	require.LessOrEqual(t, finalMemStats.HeapAlloc, maxHeap,
		"heap should not grow beyond 2x baseline (final=%d, baseline=%d, max=%d)",
		finalMemStats.HeapAlloc, baselineMemStats.HeapAlloc, maxHeap)
}

// testRelayWrapper holds a relay reference for goroutine-safe sharing.
type testRelayWrapper struct {
	relay *distribution.Relay
}
