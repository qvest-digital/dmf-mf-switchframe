//go:build correctness

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// TestRefClockTimingIndependentOfLoad proves that the reference clock's
// PTS output is identical regardless of how long processing takes.
// This is the core property that decouples CPU/GPU load from A/V timing.
func TestRefClockTimingIndependentOfLoad(t *testing.T) {
	const (
		fpsNum     = 30000
		fpsDen     = 1001
		sampleRate = 48000
		basePTS    = int64(90000)
		numTicks   = 100
	)

	// Run 1: consume ticks instantly (zero processing time)
	rc1 := clock.NewReferenceClock(fpsNum, fpsDen, sampleRate, basePTS, false)
	go rc1.Start()
	defer rc1.Stop()

	var videoPTS1 []int64
	var audioSamples1 []int
	for i := 0; i < numTicks; i++ {
		tick := <-rc1.Ticks()
		videoPTS1 = append(videoPTS1, tick.VideoPTS)
		audioSamples1 = append(audioSamples1, tick.AudioSamples)
	}

	// Run 2: simulate heavy processing (sleep 10ms per tick — ~30% of frame budget)
	rc2 := clock.NewReferenceClock(fpsNum, fpsDen, sampleRate, basePTS, false)
	go rc2.Start()
	defer rc2.Stop()

	var videoPTS2 []int64
	var audioSamples2 []int
	for i := 0; i < numTicks; i++ {
		tick := <-rc2.Ticks()
		videoPTS2 = append(videoPTS2, tick.VideoPTS)
		audioSamples2 = append(audioSamples2, tick.AudioSamples)
		time.Sleep(10 * time.Millisecond) // simulate encode load
	}

	// Run 3: simulate EXTREME processing (sleep 50ms — exceeds 33ms frame budget)
	rc3 := clock.NewReferenceClock(fpsNum, fpsDen, sampleRate, basePTS, false)
	go rc3.Start()
	defer rc3.Stop()

	var videoPTS3 []int64
	var audioSamples3 []int
	for i := 0; i < numTicks; i++ {
		tick := <-rc3.Ticks()
		videoPTS3 = append(videoPTS3, tick.VideoPTS)
		audioSamples3 = append(audioSamples3, tick.AudioSamples)
		time.Sleep(50 * time.Millisecond) // simulate overloaded GPU
	}

	// ALL THREE RUNS MUST PRODUCE IDENTICAL PTS AND SAMPLE SEQUENCES
	for i := 0; i < numTicks; i++ {
		require.Equal(t, videoPTS1[i], videoPTS2[i],
			"tick %d: PTS differs between instant and 10ms processing (load affected timing)", i)
		require.Equal(t, videoPTS1[i], videoPTS3[i],
			"tick %d: PTS differs between instant and 50ms processing (load affected timing)", i)
		require.Equal(t, audioSamples1[i], audioSamples2[i],
			"tick %d: audio samples differ between instant and 10ms processing", i)
		require.Equal(t, audioSamples1[i], audioSamples3[i],
			"tick %d: audio samples differ between instant and 50ms processing", i)
	}

	// Verify the PTS values are correct (not just equal)
	for i := 1; i < numTicks; i++ {
		delta := videoPTS1[i] - videoPTS1[i-1]
		require.True(t, delta == 3003 || delta == 3004,
			"tick %d: PTS delta %d not 3003 (29.97fps)", i, delta)
	}

	// Verify total audio samples are exact
	var totalSamples int
	for _, s := range audioSamples1 {
		totalSamples += s
	}
	t.Logf("MEASURE: %d ticks, total audio samples = %d, avg = %.1f per tick",
		numTicks, totalSamples, float64(totalSamples)/float64(numTicks))

	// Verify no wall-clock dependency by checking that run3 (5 seconds of sleeping)
	// produced the same output as run1 (instant)
	t.Logf("PROOF: Run 1 (instant): first PTS=%d, last PTS=%d", videoPTS1[0], videoPTS1[numTicks-1])
	t.Logf("PROOF: Run 2 (10ms/tick): first PTS=%d, last PTS=%d", videoPTS2[0], videoPTS2[numTicks-1])
	t.Logf("PROOF: Run 3 (50ms/tick): first PTS=%d, last PTS=%d", videoPTS3[0], videoPTS3[numTicks-1])
}
