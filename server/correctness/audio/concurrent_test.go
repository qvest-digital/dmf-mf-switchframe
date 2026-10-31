//go:build correctness

package audio

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// rate48k is the mixer's sample rate as a media-timeline rate.
var rate48k = clock.Rate{Num: 48000, Den: 1}

// period is the sample count the program clock asks the mixer for per call.
const period = 1024

// readLag is how far behind the current TAI time the program read point
// runs, in samples: a feeder pushes a chunk once its media time is due, so
// the reader finds it buffered two periods later.
const readLag = 2 * period

// nowIndex returns the 48 kHz media-timeline sample index of now.
func nowIndex() uint64 { return clock.TimestampToIndex(rate48k, clock.TAINow()) }

// runProgramClock stands in for the reference clock: until stop is closed
// it calls m.ProduceSamples for every period whose end is readLag behind
// now, on a contiguous read point, then AdvanceCrossfade. The returned
// channel is closed when the loop has exited.
func runProgramClock(m *audio.Mixer, stop <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		read := nowIndex() - readLag
		for {
			for read+period <= nowIndex()-readLag {
				m.ProduceSamples(read, period, clock.IndexPTS(rate48k, read))
				m.AdvanceCrossfade()
				read += period
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return done
}

// feedRealtime ingests pcm (interleaved stereo, looped) for key in chunks
// of chunk samples, each stamped with its media time and pushed once that
// time is due, as a live source delivers. It returns the number of chunks
// pushed once stop is closed, or once pcm is used up when loop is false.
func feedRealtime(m *audio.Mixer, key string, pcm []float32, chunk int, loop bool, stop <-chan struct{}) int {
	const ch = 2
	start := nowIndex()
	pushed := 0
	for offset := 0; ; offset += chunk * ch {
		if offset+chunk*ch > len(pcm) {
			if !loop {
				return pushed
			}
			offset = 0
		}
		idx := start + uint64(pushed*chunk)
		for nowIndex() < idx {
			select {
			case <-stop:
				return pushed
			case <-time.After(time.Millisecond):
			}
		}
		select {
		case <-stop:
			return pushed
		default:
		}
		m.IngestPCM(key, pcm[offset:offset+chunk*ch], clock.IndexPTS(rate48k, idx), ch)
		pushed++
	}
}

// generateStereoSine produces interleaved stereo float32 PCM at the given frequency.
func generateStereoSine(freqHz float64, sampleRate int, durationSec float64) []float32 {
	mono := testutil.GenerateSineWavePCM(freqHz, -6, sampleRate, durationSec)
	stereo := make([]float32, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s
		stereo[i*2+1] = s
	}
	return stereo
}

// pcmStats computes RMS, max absolute value, and checks for NaN/Inf.
func pcmStats(pcm []float32) (rms float64, maxAbs float64, hasNaN bool, hasInf bool) {
	if len(pcm) == 0 {
		return 0, 0, false, false
	}
	var sumSq float64
	for _, s := range pcm {
		v := float64(s)
		if math.IsNaN(v) {
			hasNaN = true
		}
		if math.IsInf(v, 0) {
			hasInf = true
		}
		abs := math.Abs(v)
		if abs > maxAbs {
			maxAbs = abs
		}
		sumSq += v * v
	}
	rms = math.Sqrt(sumSq / float64(len(pcm)))
	return
}

// newTestMixer creates a mixer with raw PCM capture, driven by a program
// clock (runProgramClock) until the test ends. Returns the mixer and a
// function to retrieve all captured PCM.
func newTestMixer(t *testing.T) (*audio.Mixer, func() []float32) {
	t.Helper()

	var mu sync.Mutex
	var captured []float32

	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})

	sink := audio.RawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		captured = append(captured, pcm...)
		mu.Unlock()
	})
	m.SetRawAudioSink(sink)

	stop := make(chan struct{})
	done := runProgramClock(m, stop)
	t.Cleanup(func() {
		close(stop)
		<-done
	})

	getCaptured := func() []float32 {
		mu.Lock()
		defer mu.Unlock()
		out := make([]float32, len(captured))
		copy(out, captured)
		return out
	}

	return m, getCaptured
}

// ==========================================================================
// 1. TestMixerConcurrentIngestFromMultipleSources
// ==========================================================================

func TestMixerConcurrentIngestFromMultipleSources(t *testing.T) {
	t.Parallel()

	m, getCaptured := newTestMixer(t)
	defer m.Close()

	// Add 4 channels and activate them
	sources := []struct {
		key    string
		freqHz float64
	}{
		{"ch1", 440},
		{"ch2", 880},
		{"ch3", 1320},
		{"ch4", 1760},
	}

	for _, s := range sources {
		m.AddChannel(s.key)
		m.SetActive(s.key, true)
	}

	const sampleRate = 48000
	const durationSec = 1.0

	// 4 goroutines feed simultaneously for 1 second
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(key string, freq float64) {
			defer wg.Done()
			pcm := generateStereoSine(freq, sampleRate, durationSec)
			feedRealtime(m, key, pcm, 1024, false, nil)
		}(s.key, s.freqHz)
	}

	wg.Wait()

	// Give the program clock time to drain remaining ring buffer data
	time.Sleep(200 * time.Millisecond)

	output := getCaptured()
	t.Logf("MEASURE: total captured samples = %d", len(output))

	require.Greater(t, len(output), 0, "should have captured output PCM")

	rms, maxAbs, hasNaN, hasInf := pcmStats(output)
	t.Logf("MEASURE: output RMS = %.4f, sample count = %d, max abs = %.4f", rms, len(output), maxAbs)

	assert.False(t, hasNaN, "output contains NaN samples")
	assert.False(t, hasInf, "output contains Inf samples")
	assert.Greater(t, rms, 0.001, "output should not be silence (RMS > 0.001)")
	// All samples should be within [-1.0, 1.0] after limiter
	assert.LessOrEqual(t, maxAbs, 1.0+0.01, "all samples should be in [-1.0, 1.0] (got max abs %.4f)", maxAbs)
}

// ==========================================================================
// 2. TestMixerAddRemoveWhileMixing
// ==========================================================================

func TestMixerAddRemoveWhileMixing(t *testing.T) {
	t.Parallel()

	m, getCaptured := newTestMixer(t)
	defer m.Close()

	const sampleRate = 48000

	// Start with ch1 and ch2 feeding
	m.AddChannel("ch1")
	m.SetActive("ch1", true)
	m.AddChannel("ch2")
	m.SetActive("ch2", true)

	stopCh1 := make(chan struct{})
	stopCh2 := make(chan struct{})
	stopCh3 := make(chan struct{})
	stopCh4 := make(chan struct{})

	feedChannel := func(key string, freqHz float64, stop <-chan struct{}) {
		pcm := generateStereoSine(freqHz, sampleRate, 5.0) // long buffer
		feedRealtime(m, key, pcm, 1024, true, stop)
	}

	// Start feeding ch1 and ch2
	go feedChannel("ch1", 440, stopCh1)
	go feedChannel("ch2", 880, stopCh2)

	// Capture RMS at intervals
	type rmsPoint struct {
		timeMs int
		rms    float64
	}
	var rmsPoints []rmsPoint
	var rmsMu sync.Mutex

	sampleRMS := func(timeMs int) {
		output := getCaptured()
		if len(output) > 0 {
			r, _, _, _ := pcmStats(output)
			rmsMu.Lock()
			rmsPoints = append(rmsPoints, rmsPoint{timeMs, r})
			rmsMu.Unlock()
		}
	}

	// t=0ms: both ch1 and ch2 feeding
	time.Sleep(200 * time.Millisecond)
	sampleRMS(200)

	// t=200ms: add ch3
	m.AddChannel("ch3")
	m.SetActive("ch3", true)
	go feedChannel("ch3", 1320, stopCh3)
	time.Sleep(200 * time.Millisecond)
	sampleRMS(400)

	// t=400ms: remove ch1
	close(stopCh1)
	time.Sleep(20 * time.Millisecond) // let feed goroutine stop
	m.RemoveChannel("ch1")
	time.Sleep(200 * time.Millisecond)
	sampleRMS(600)

	// t=600ms: add ch4
	m.AddChannel("ch4")
	m.SetActive("ch4", true)
	go feedChannel("ch4", 1760, stopCh4)
	time.Sleep(200 * time.Millisecond)
	sampleRMS(800)

	// t=800ms: remove ch2
	close(stopCh2)
	time.Sleep(20 * time.Millisecond)
	m.RemoveChannel("ch2")
	time.Sleep(200 * time.Millisecond)
	sampleRMS(1000)

	// Continue for another 200ms
	time.Sleep(200 * time.Millisecond)
	sampleRMS(1200)

	// Stop remaining feeders
	close(stopCh3)
	close(stopCh4)

	// Give the program clock time to flush
	time.Sleep(100 * time.Millisecond)

	output := getCaptured()

	rmsMu.Lock()
	defer rmsMu.Unlock()

	t.Logf("MEASURE: RMS at 200ms intervals: %+v", rmsPoints)
	t.Logf("MEASURE: total captured samples = %d", len(output))

	rms, _, hasNaN, hasInf := pcmStats(output)
	assert.False(t, hasNaN, "output contains NaN samples")
	assert.False(t, hasInf, "output contains Inf samples")

	// Output should have signal at every sampled point
	for _, pt := range rmsPoints {
		assert.Greater(t, pt.rms, 0.0005,
			"RMS should be > 0.0005 at time %dms (got %.6f)", pt.timeMs, pt.rms)
	}
	t.Logf("MEASURE: final output RMS = %.4f", rms)
}

// ==========================================================================
// 3. TestMixerCrossfadeConcurrentWithIngest
// ==========================================================================

func TestMixerCrossfadeConcurrentWithIngest(t *testing.T) {
	t.Parallel()

	// No program clock: ProduceSamples is called here, deterministically.
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer m.Close()

	const sampleRate = 48000
	const chunkSize = 1024 * 2 // stereo: 1024 samples * 2 channels

	// camA is active (on program), camB is inactive
	m.AddChannel("camA")
	m.SetActive("camA", true)
	m.SetAFV("camA", true)

	m.AddChannel("camB")
	m.SetActive("camB", false) // not on program yet
	m.SetAFV("camB", true)

	// Pre-generate audio buffers (5 seconds each)
	pcmA := generateStereoSine(440, sampleRate, 5.0)
	pcmB := generateStereoSine(880, sampleRate, 5.0)

	// Phase 1: Concurrent ingest stress test.
	// Multiple goroutines hammer IngestPCM simultaneously to verify no
	// data races, panics, or corruption under concurrent access.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	feedStart := nowIndex()

	for _, feed := range []struct {
		key string
		pcm []float32
	}{
		{"camA", pcmA},
		{"camB", pcmB},
	} {
		wg.Add(1)
		go func(key string, pcm []float32) {
			defer wg.Done()
			offset := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				if offset+chunkSize > len(pcm) {
					offset = 0
				}
				pts := clock.IndexPTS(rate48k, feedStart+uint64(offset/2))
				m.IngestPCM(key, pcm[offset:offset+chunkSize], pts, 2)
				offset += chunkSize
				time.Sleep(time.Millisecond)
			}
		}(feed.key, feed.pcm)
	}

	// Let concurrent feeders run for a bit to exercise the race detector
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Phase 2: Deterministic crossfade verification.
	// Now that concurrent ingest is done, refill ring buffers with a known
	// amount of audio and produce output without any timing dependency.

	// Fill each ring buffer with enough data for the entire test.
	// Ring buffer capacity is 10*1024*2 = 20480 samples.
	// We'll produce 8 ticks * 1024 samples * 2 channels = 16384 values per tick.
	// Fill with ~9K stereo samples (18K values) to stay within capacity.
	// The fill is stamped as the audio from read on, where the reads are.
	const fillSamples = 9 * 1024 * 2 // stereo values to push
	read := nowIndex()
	m.IngestPCM("camA", pcmA[:fillSamples], clock.IndexPTS(rate48k, read), 2)
	m.IngestPCM("camB", pcmB[:fillSamples], clock.IndexPTS(rate48k, read), 2)

	// Produce pre-crossfade output to verify camA signal is present.
	const samplesPerTick = 1024
	preCrossfade := m.ProduceSamples(read, samplesPerTick, clock.IndexPTS(rate48k, read))
	read += samplesPerTick
	require.Greater(t, len(preCrossfade), 0, "pre-crossfade should produce output")

	preRMS, _, _, _ := pcmStats(preCrossfade)
	t.Logf("MEASURE: pre-crossfade RMS = %.4f", preRMS)
	assert.Greater(t, preRMS, 0.01, "pre-crossfade should have non-trivial signal from camA")

	// Trigger AFV crossfade: camB becomes program
	m.OnProgramChange("camB")

	// Produce samples deterministically through the 4-tick crossfade ramp
	// and a few ticks beyond. No timing dependency -- purely sequential.
	var allOutput []float32
	for i := 0; i < 8; i++ {
		samples := m.ProduceSamples(read, samplesPerTick, clock.IndexPTS(rate48k, read))
		read += samplesPerTick
		m.AdvanceCrossfade()
		allOutput = append(allOutput, samples...)
	}

	require.Greater(t, len(allOutput), 0, "should have produced output")

	rms, maxAbs, hasNaN, hasInf := pcmStats(allOutput)
	t.Logf("MEASURE: output RMS = %.4f, max abs = %.4f, sample count = %d", rms, maxAbs, len(allOutput))

	assert.False(t, hasNaN, "output contains NaN samples")
	assert.False(t, hasInf, "output contains Inf samples")

	// Check for clicks per-channel (L and R separately) to avoid false
	// positives from interleaved stereo where adjacent L/R samples from
	// different-frequency sine waves naturally differ.
	maxClickL := float64(0)
	maxClickR := float64(0)
	for i := 2; i+1 < len(allOutput); i += 2 {
		deltaL := math.Abs(float64(allOutput[i]) - float64(allOutput[i-2]))
		deltaR := math.Abs(float64(allOutput[i+1]) - float64(allOutput[i-1]))
		if deltaL > maxClickL {
			maxClickL = deltaL
		}
		if deltaR > maxClickR {
			maxClickR = deltaR
		}
	}
	t.Logf("MEASURE: max click L = %.4f, R = %.4f", maxClickL, maxClickR)

	// Per-channel delta should be < 0.5 (no hard discontinuities).
	// Equal-power crossfade between two sine waves produces smooth transitions.
	assert.Less(t, maxClickL, 0.5, "left channel max delta should be < 0.5 (no clicks)")
	assert.Less(t, maxClickR, 0.5, "right channel max delta should be < 0.5 (no clicks)")

	// Check for silence runs > 5ms per channel (5ms * 48000 = 240 per-channel samples).
	maxSilenceRun := 0
	currentSilence := 0
	const silenceThreshold = 0.0001
	for i := 0; i < len(allOutput); i += 2 {
		// Check left channel (right mirrors it in generateStereoSine)
		if math.Abs(float64(allOutput[i])) < silenceThreshold {
			currentSilence++
			if currentSilence > maxSilenceRun {
				maxSilenceRun = currentSilence
			}
		} else {
			currentSilence = 0
		}
	}
	silenceRunMs := float64(maxSilenceRun) / float64(sampleRate) * 1000.0
	t.Logf("MEASURE: max silence run = %d per-channel samples (%.2f ms)", maxSilenceRun, silenceRunMs)

	// No silence gap > 5ms. With deterministic production from pre-filled
	// ring buffers, there should be no underruns.
	const maxAllowedSilenceMs = 5.0
	assert.Less(t, silenceRunMs, maxAllowedSilenceMs,
		"silence gap should be < %.0fms (got %.2fms = %d per-channel samples)", maxAllowedSilenceMs, silenceRunMs, maxSilenceRun)

	// Verify crossfade completed (no longer active)
	assert.False(t, m.IsInTransitionCrossfade(), "crossfade should have completed after 8 ticks (4 needed)")
}

// ==========================================================================
// 4. TestMixerRingBufferStarvation
// ==========================================================================

func TestMixerRingBufferStarvation(t *testing.T) {
	t.Parallel()

	m, getCaptured := newTestMixer(t)
	defer m.Close()

	const sampleRate = 48000
	const chunkSize = 1024 * 2

	// Add channel but don't feed it -- mixer should read silence from empty ring buffer
	m.AddChannel("starved")
	m.SetActive("starved", true)

	// Wait 500ms -- the program clock is running, reading from the empty ring buffer
	time.Sleep(500 * time.Millisecond)

	outputBeforeFeed := getCaptured()
	rmsBeforeFeed, _, hasNaN1, hasInf1 := pcmStats(outputBeforeFeed)
	t.Logf("MEASURE: before feed - RMS = %.6f, sample count = %d", rmsBeforeFeed, len(outputBeforeFeed))

	assert.False(t, hasNaN1, "output before feed contains NaN")
	assert.False(t, hasInf1, "output before feed contains Inf")

	// Count frames before any signal appears
	framesBeforeSignal := 0
	const signalThreshold = 0.001
	for i := 0; i+chunkSize <= len(outputBeforeFeed); i += chunkSize {
		chunk := outputBeforeFeed[i : i+chunkSize]
		chunkRMS, _, _, _ := pcmStats(chunk)
		if chunkRMS > signalThreshold {
			break
		}
		framesBeforeSignal++
	}

	// Now start feeding
	pcm := generateStereoSine(440, sampleRate, 1.0)
	stop := make(chan struct{})
	go feedRealtime(m, "starved", pcm, 1024, true, stop)

	// Wait for signal to propagate through ring buffer
	time.Sleep(500 * time.Millisecond)
	close(stop)
	time.Sleep(100 * time.Millisecond)

	outputAfterFeed := getCaptured()
	rmsAfterFeed, _, hasNaN2, hasInf2 := pcmStats(outputAfterFeed)
	t.Logf("MEASURE: after feed - RMS = %.6f, sample count = %d", rmsAfterFeed, len(outputAfterFeed))
	t.Logf("MEASURE: frames before signal = %d, signal onset RMS = %.4f", framesBeforeSignal, rmsAfterFeed)

	assert.False(t, hasNaN2, "output after feed contains NaN")
	assert.False(t, hasInf2, "output after feed contains Inf")

	// After feeding, RMS should increase (signal present)
	assert.Greater(t, rmsAfterFeed, rmsBeforeFeed,
		"RMS should increase after feeding (before=%.6f, after=%.6f)", rmsBeforeFeed, rmsAfterFeed)
}

// ==========================================================================
// 5. TestMixerHighContentionIngest
// ==========================================================================

func TestMixerHighContentionIngest(t *testing.T) {
	t.Parallel()

	m, getCaptured := newTestMixer(t)
	defer m.Close()

	const sampleRate = 48000
	const numChannels = 8

	// Add 8 channels
	for i := 0; i < numChannels; i++ {
		key := fmt.Sprintf("src%d", i)
		m.AddChannel(key)
		m.SetActive(key, true)
	}

	var wg sync.WaitGroup
	var totalIngests atomic.Int64
	stop := make(chan struct{})

	// 8 goroutines all feeding at high rate: 256-sample chunks, four
	// ingests per program period.
	for i := 0; i < numChannels; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("src%d", idx)
			freq := 220.0 * float64(idx+1)
			pcm := generateStereoSine(freq, sampleRate, 5.0)
			totalIngests.Add(int64(feedRealtime(m, key, pcm, 256, true, stop)))
		}(i)
	}

	// Run for 2 seconds
	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()

	// Flush
	time.Sleep(200 * time.Millisecond)

	output := getCaptured()
	ingests := totalIngests.Load()

	rms, maxAbs, hasNaN, hasInf := pcmStats(output)
	t.Logf("MEASURE: total ingests = %d, output samples = %d", ingests, len(output))
	t.Logf("MEASURE: output RMS = %.4f, max abs = %.4f", rms, maxAbs)

	assert.False(t, hasNaN, "output contains NaN samples")
	assert.False(t, hasInf, "output contains Inf samples")
	assert.Greater(t, len(output), 0, "should have produced output")
	assert.Greater(t, rms, 0.001, "output should not be silence")
	assert.Greater(t, ingests, int64(100), "should have completed many ingests (got %d)", ingests)

	// Verify limiter kept output in range
	assert.LessOrEqual(t, maxAbs, 1.0+0.01,
		"limiter should keep output within [-1, 1] (got max abs %.4f)", maxAbs)
}
