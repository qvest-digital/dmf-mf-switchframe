package switcher

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/transition"
)

// TestStress_RapidCuts exercises 5 cuts in 1 second with concurrent frame
// injection from 4 sources. Verifies: final program source is correct,
// seq incremented correctly, no panics under the race detector.
func TestStress_RapidCuts(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	// Register 4 sources.
	sources := []string{"cam1", "cam2", "cam3", "cam4"}
	relays := make(map[string]*testRelay, 4)
	for _, key := range sources {
		relay := newTestRelay()
		relays[key] = relay
		sw.RegisterSource(key, relay)
	}

	// Initial cut to cam1.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start concurrent frame injection from all sources.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var frameWg sync.WaitGroup
	for _, key := range sources {
		key := key
		relay := relays[key]
		frameWg.Add(1)
		go func() {
			defer frameWg.Done()
			pts := int64(0)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					relay.BroadcastVideo(&media.VideoFrame{
						PTS:        pts,
						IsKeyframe: pts%100000 == 0,
						WireData:   []byte{0x01},
					})
					relay.BroadcastAudio(&media.AudioFrame{
						PTS:        pts,
						Data:       []byte{0xAA},
						SampleRate: 48000,
						Channels:   2,
					})
					pts += 33333 // ~30fps
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	// Perform 5 rapid cuts with 200ms spacing, cycling through sources.
	cutSequence := []string{"cam2", "cam3", "cam4", "cam1", "cam3"}
	for _, target := range cutSequence {
		require.NoError(t, sw.Cut(context.Background(), target))
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	frameWg.Wait()

	// Verify final state.
	state := sw.State()
	require.Equal(t, "cam3", state.ProgramSource, "final program source should be cam3")

	// 1 initial cut + 5 rapid cuts = 6 total cuts, but some may be no-ops
	// if cutting to the same source (cam1 -> cam2 -> cam3 -> cam4 -> cam1 -> cam3).
	// All are to different sources, so all increment seq.
	// Seq: initial=0, cut(cam1)=1, cut(cam2)=2, cut(cam3)=3, cut(cam4)=4, cut(cam1)=5, cut(cam3)=6
	require.Equal(t, uint64(6), state.Seq, "seq should be 6 after 6 successful cuts")
}

// testRelay is a type alias for readability (the real type is distribution.Relay).
type testRelay = distribution.Relay

// TestStress_CutDuringTransition starts a dissolve transition and then issues
// a Cut() before it completes. Verifies: transition is aborted cleanly, cut
// completes, program is the new source, no leaked goroutines.
func TestStress_CutDuringTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	// Cut to cam1 and clear IDR gate.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	goroutinesBefore := runtime.NumGoroutine()

	// Start a long dissolve from cam1 -> cam2.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Verify transition is active.
	state := sw.State()
	require.True(t, state.InTransition, "should be in transition")

	// Feed a few frames to get the transition engine running.
	for i := 0; i < 5; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(100 + i*33), IsKeyframe: i == 0, WireData: []byte{0x01}})
		cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(200 + i*33), IsKeyframe: i == 0, WireData: []byte{0x02}})
	}
	time.Sleep(10 * time.Millisecond)

	// Abort the transition and immediately cut to cam3. In a real switcher,
	// pressing CUT during a dissolve aborts the transition and hard-cuts.
	sw.AbortTransition()
	require.NoError(t, sw.Cut(context.Background(), "cam3"))

	// Verify: transition aborted, cam3 is on program.
	state = sw.State()
	require.False(t, state.InTransition, "transition should be aborted after abort+cut")
	require.Equal(t, "cam3", state.ProgramSource, "program should be cam3 after cut")

	// Allow goroutines to settle.
	time.Sleep(100 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()

	// Allow a small margin for runtime fluctuations (GC, timers, etc).
	leaked := goroutinesAfter - goroutinesBefore
	require.LessOrEqual(t, leaked, 5,
		"should not leak many goroutines: before=%d after=%d", goroutinesBefore, goroutinesAfter)
}

// TestStress_20Sources registers 20 sources simultaneously, sets preview and
// cuts between them. Verifies: all 20 sources appear in state, frame routing
// works for an arbitrary source.
func TestStress_20Sources(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	const numSources = 20

	// Register 20 sources concurrently.
	var regWg sync.WaitGroup
	relays := make(map[string]*testRelay, numSources)
	var relayMu sync.Mutex

	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%02d", i+1)
		relay := newTestRelay()
		relayMu.Lock()
		relays[key] = relay
		relayMu.Unlock()

		regWg.Add(1)
		go func() {
			defer regWg.Done()
			sw.RegisterSource(key, relay)
		}()
	}
	regWg.Wait()

	// Verify all 20 sources appear in state.
	state := sw.State()
	require.Len(t, state.Sources, numSources, "should have 20 sources registered")

	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%02d", i+1)
		_, exists := state.Sources[key]
		require.True(t, exists, "source %q should exist in state", key)
	}

	// Cut to cam10 and verify frame routing.
	require.NoError(t, sw.Cut(context.Background(), "cam10"))
	relays["cam10"].BroadcastVideo(&media.VideoFrame{PTS: 1000, IsKeyframe: true, WireData: []byte{0x10}})

	capture.mu.Lock()
	require.GreaterOrEqual(t, len(capture.videos), 1, "should receive at least one frame from cam10")
	require.Equal(t, int64(1000), capture.videos[len(capture.videos)-1].PTS)
	capture.mu.Unlock()

	// Set preview to cam15.
	require.NoError(t, sw.SetPreview(context.Background(), "cam15"))
	state = sw.State()
	require.Equal(t, "cam15", state.PreviewSource)

	// Cut to cam20 and verify it becomes program.
	require.NoError(t, sw.Cut(context.Background(), "cam20"))
	relays["cam20"].BroadcastVideo(&media.VideoFrame{PTS: 2000, IsKeyframe: true, WireData: []byte{0x20}})

	state = sw.State()
	require.Equal(t, "cam20", state.ProgramSource)
	require.Equal(t, "cam10", state.PreviewSource, "previous program should become preview")
}

// TestStress_SimultaneousOutputs verifies that rapid cuts with a mock program
// viewer attached (simulating recording/SRT) do not deadlock. All operations
// must complete within 5 seconds.
func TestStress_SimultaneousOutputs(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()

	// Attach multiple mock viewers to simulate recording + SRT output.
	recorder := newMockProgramViewer("recorder")
	srtOutput := newMockProgramViewer("srt-output")
	programRelay.AddViewer(recorder)
	programRelay.AddViewer(srtOutput)

	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start concurrent frame injection.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var frameWg sync.WaitGroup
	injectFrames := func(relay *testRelay) {
		frameWg.Add(1)
		go func() {
			defer frameWg.Done()
			pts := int64(0)
			frame := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					relay.BroadcastVideo(&media.VideoFrame{
						PTS:        pts,
						IsKeyframe: frame%30 == 0,
						WireData:   []byte{0x01},
					})
					pts += 33333
					frame++
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	injectFrames(cam1Relay)
	injectFrames(cam2Relay)
	injectFrames(cam3Relay)

	// Use require.Eventually to verify rapid cuts complete without deadlock.
	done := make(chan struct{})
	go func() {
		defer close(done)
		targets := []string{"cam2", "cam3", "cam1", "cam2", "cam3", "cam1", "cam2", "cam3"}
		for _, target := range targets {
			_ = sw.Cut(context.Background(), target)
			time.Sleep(50 * time.Millisecond)
		}
	}()

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 5*time.Second, 50*time.Millisecond, "rapid cuts with simultaneous outputs should complete within 5s")

	cancel()
	frameWg.Wait()

	// Verify both output viewers received frames.
	recorder.mu.Lock()
	recCount := len(recorder.videos)
	recorder.mu.Unlock()

	srtOutput.mu.Lock()
	srtCount := len(srtOutput.videos)
	srtOutput.mu.Unlock()

	require.Greater(t, recCount, 0, "recorder should have received frames")
	require.Greater(t, srtCount, 0, "SRT output should have received frames")
}

// TestStress_ConcurrentStateReads exercises concurrent state mutations and reads
// for 2 seconds. Three goroutines run simultaneously: one doing rapid cuts,
// one continuously reading state, and one registering/unregistering sources.
// The -race flag catches any data races.
func TestStress_ConcurrentStateReads(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Register initial sources.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear IDR gate for initial source.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var racePanics atomic.Int64

	// Goroutine 1: Rapid cuts between cam1 and cam2.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				racePanics.Add(1)
			}
		}()
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i%2 == 0 {
					_ = sw.Cut(context.Background(), "cam2")
				} else {
					_ = sw.Cut(context.Background(), "cam1")
				}
				i++
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Goroutine 2: Continuous state reads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				racePanics.Add(1)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				state := sw.State()
				// Access various state fields to trigger potential races.
				_ = state.ProgramSource
				_ = state.PreviewSource
				_ = state.Seq
				_ = state.InTransition
				_ = len(state.Sources)
				_ = len(state.TallyState)
				// Also exercise DebugSnapshot which reads different paths.
				_ = sw.DebugSnapshot()
			}
		}
	}()

	// Goroutine 3: Register/unregister dynamic sources.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				racePanics.Add(1)
			}
		}()
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				key := fmt.Sprintf("dynamic-%d", i%5)
				relay := newTestRelay()
				sw.RegisterSource(key, relay)
				// Give the source some time to exist, then unregister.
				time.Sleep(5 * time.Millisecond)
				sw.UnregisterSource(key)
			}
		}
	}()

	wg.Wait()

	require.Equal(t, int64(0), racePanics.Load(), "no panics should occur during concurrent access")

	// Final state should be consistent.
	finalState := sw.State()
	require.True(t,
		finalState.ProgramSource == "cam1" || finalState.ProgramSource == "cam2",
		"program source should be cam1 or cam2, got %q", finalState.ProgramSource)
}

// TestStress_AllChannelsMixing exercises 8 sources all sending audio
// simultaneously with all channels unmuted and AFV enabled. Verifies:
// mix cycle completes, output frames are produced, no deadline timeout
// causes a hang.
func TestStress_AllChannelsMixing(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	// Create mixer wired to program relay.
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

	const numSources = 8
	relays := make(map[string]*testRelay, numSources)

	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%d", i+1)
		relay := newTestRelay()
		relays[key] = relay
		sw.RegisterSource(key, relay)
		mixer.AddChannel(key)
		require.NoError(t, mixer.SetAFV(key, true))
	}

	// Cut to cam1 so we have a program source. AFV auto-activates cam1.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear IDR gate.
	relays["cam1"].BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// All 8 sources send audio simultaneously.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var frameWg sync.WaitGroup
	for i := 0; i < numSources; i++ {
		key := fmt.Sprintf("cam%d", i+1)
		relay := relays[key]
		frameWg.Add(1)
		go func() {
			defer frameWg.Done()
			pts := int64(0)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					relay.BroadcastAudio(&media.AudioFrame{
						PTS:        pts,
						Data:       []byte{0xAA, 0xBB, 0xCC, 0xDD},
						SampleRate: 48000,
						Channels:   2,
					})
					pts += 23000 // ~1 AAC frame duration in microseconds
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// Let audio flow for 1 second.
	time.Sleep(1 * time.Second)

	cancel()
	frameWg.Wait()

	// Allow any pending mix cycles to flush.
	time.Sleep(100 * time.Millisecond)

	// Verify: audio was produced (passthrough or mixed). With only cam1 active
	// via AFV and at 0dB, passthrough should be used. Either way, frames should
	// have reached the capture viewer.
	capture.mu.Lock()
	audioCount := len(capture.audios)
	capture.mu.Unlock()

	require.Greater(t, audioCount, 0,
		"should have received audio output frames from the mixer (got %d)", audioCount)
}
