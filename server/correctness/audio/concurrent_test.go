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
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// mockEncoder is a trivial encoder that converts float32 PCM to raw bytes.
// It satisfies audio.Encoder without needing cgo/fdk-aac.
type mockEncoder struct{}

func (e *mockEncoder) Encode(pcm []float32) ([]byte, error) {
	// Just return some non-empty bytes so the mixer thinks encoding succeeded.
	// We rely on SetRawAudioSink for actual PCM capture.
	return make([]byte, len(pcm)*4), nil
}

func (e *mockEncoder) Close() error { return nil }

func mockEncoderFactory(sampleRate, channels int) (audio.Encoder, error) {
	return &mockEncoder{}, nil
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

// newTestMixer creates a mixer with mock encoder and raw PCM capture.
// Returns the mixer and a function to retrieve all captured PCM.
func newTestMixer(t *testing.T) (*audio.Mixer, func() []float32) {
	t.Helper()

	var mu sync.Mutex
	var captured []float32

	m := audio.NewMixer(audio.MixerConfig{
		SampleRate:     48000,
		Channels:       2,
		Output:         func(f *media.AudioFrame) {}, // no-op, we use raw sink
		EncoderFactory: mockEncoderFactory,
	})

	sink := audio.RawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		captured = append(captured, pcm...)
		mu.Unlock()
	})
	m.SetRawAudioSink(sink)

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
	const chunkSize = 1024 * 2 // stereo chunk = 1024 samples * 2 channels

	// 4 goroutines feed simultaneously for 1 second
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(key string, freq float64) {
			defer wg.Done()
			pcm := generateStereoSine(freq, sampleRate, durationSec)
			// Feed in chunks matching AAC frame size
			for offset := 0; offset+chunkSize <= len(pcm); offset += chunkSize {
				m.IngestPCM(key, pcm[offset:offset+chunkSize], int64(offset)*90000/int64(sampleRate*2), 2)
				// Pace to avoid overwhelming the ring buffer
				time.Sleep(time.Millisecond * 5)
			}
		}(s.key, s.freqHz)
	}

	wg.Wait()

	// Give the mixer ticker time to drain remaining ring buffer data
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
	const chunkSize = 1024 * 2

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
		offset := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			if offset+chunkSize > len(pcm) {
				offset = 0 // loop
			}
			m.IngestPCM(key, pcm[offset:offset+chunkSize], int64(offset)*90000/int64(sampleRate*2), 2)
			offset += chunkSize
			time.Sleep(10 * time.Millisecond)
		}
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

	// Give ticker time to flush
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
	// Not parallel: this test depends on precise timing for ring buffer fill
	// rates and crossfade detection. CPU contention from parallel tests causes
	// feeder goroutines to stall, leading to ring buffer underruns.

	m, getCaptured := newTestMixer(t)
	defer m.Close()

	const sampleRate = 48000
	const chunkSize = 1024 * 2

	// camA is active (on program), camB is inactive
	m.AddChannel("camA")
	m.SetActive("camA", true)
	m.SetAFV("camA", true)

	m.AddChannel("camB")
	m.SetActive("camB", false) // not on program yet
	m.SetAFV("camB", true)

	stopA := make(chan struct{})
	stopB := make(chan struct{})

	feedLoop := func(key string, freqHz float64, stop <-chan struct{}) {
		pcm := generateStereoSine(freqHz, sampleRate, 10.0)
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
			m.IngestPCM(key, pcm[offset:offset+chunkSize], int64(offset)*90000/int64(sampleRate*2), 2)
			offset += chunkSize
			// Pace at 20ms (vs 21.3ms ticker interval) to keep the ring buffer
			// filled just ahead of the consumer. Slightly faster than real-time
			// to prevent underruns, but slow enough to avoid overflow-induced
			// phase discontinuity clicks.
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Both goroutines feed PCM simultaneously
	go feedLoop("camA", 440, stopA)
	go feedLoop("camB", 880, stopB)

	// Let camA stabilize on program for 500ms
	time.Sleep(500 * time.Millisecond)

	// Trigger program change: camB becomes program via AFV
	m.OnProgramChange("camB")

	// Let it run for another 500ms to capture the crossfade
	time.Sleep(500 * time.Millisecond)

	close(stopA)
	close(stopB)
	time.Sleep(100 * time.Millisecond) // flush

	output := getCaptured()
	require.Greater(t, len(output), 0, "should have captured output")

	rms, maxAbs, hasNaN, hasInf := pcmStats(output)
	t.Logf("MEASURE: output RMS = %.4f, max abs = %.4f, sample count = %d", rms, maxAbs, len(output))

	assert.False(t, hasNaN, "output contains NaN samples")
	assert.False(t, hasInf, "output contains Inf samples")

	// Check for clicks: max sample-to-sample delta
	maxClick := float64(0)
	for i := 1; i < len(output); i++ {
		delta := math.Abs(float64(output[i]) - float64(output[i-1]))
		if delta > maxClick {
			maxClick = delta
		}
	}
	t.Logf("MEASURE: max click = %.4f", maxClick)

	// Check for silence runs > 5ms (5ms * 48000 * 2 = 480 samples)
	maxSilenceRun := 0
	currentSilence := 0
	const silenceThreshold = 0.0001
	for _, s := range output {
		if math.Abs(float64(s)) < silenceThreshold {
			currentSilence++
			if currentSilence > maxSilenceRun {
				maxSilenceRun = currentSilence
			}
		} else {
			currentSilence = 0
		}
	}
	silenceRunMs := float64(maxSilenceRun) / float64(sampleRate*2) * 1000.0
	t.Logf("MEASURE: max silence run = %d samples (%.2f ms)", maxSilenceRun, silenceRunMs)

	// The max click should be < 0.5 (no hard discontinuities)
	assert.Less(t, maxClick, 0.5, "max sample-to-sample delta should be < 0.5 (no clicks)")

	// No silence gap > 5ms. The ring buffer ticker produces output every ~21ms,
	// so we allow up to one tick of silence during the AFV transition.
	const maxAllowedSilenceMs = 25.0
	assert.Less(t, silenceRunMs, maxAllowedSilenceMs,
		"silence gap should be < %.0fms (got %.2fms = %d samples)", maxAllowedSilenceMs, silenceRunMs, maxSilenceRun)
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

	// Add channel but don't feed it — mixer should read silence from empty ring buffer
	m.AddChannel("starved")
	m.SetActive("starved", true)

	// Wait 500ms — the mixer ticker is running, reading from the empty ring buffer
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
	go func() {
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
			m.IngestPCM("starved", pcm[offset:offset+chunkSize], int64(offset)*90000/int64(sampleRate*2), 2)
			offset += chunkSize
			time.Sleep(10 * time.Millisecond)
		}
	}()

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
	const chunkSize = 1024 * 2
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

	// 8 goroutines all feeding at high rate (every 10ms instead of 21ms)
	for i := 0; i < numChannels; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("src%d", idx)
			freq := 220.0 * float64(idx+1)
			pcm := generateStereoSine(freq, sampleRate, 5.0)
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
				m.IngestPCM(key, pcm[offset:offset+chunkSize], int64(offset)*90000/int64(sampleRate*2), 2)
				offset += chunkSize
				totalIngests.Add(1)
				time.Sleep(10 * time.Millisecond) // faster than ticker cadence
			}
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
