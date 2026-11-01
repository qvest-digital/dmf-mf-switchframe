package clock

import (
	"runtime"
	"testing"
	"time"
)

func TestReferenceClockTickPTS(t *testing.T) {
	// 30fps: PTS delta = 90000 * 1 / 30 = 3000
	c := NewReferenceClock(30, 1, 48000, 0, false)
	c.Start()
	defer c.Stop()

	var prev int64 = -3000 // so first delta check works
	for i := 0; i < 100; i++ {
		tick := <-c.Ticks()
		if tick.Number != int64(i) {
			t.Fatalf("tick %d: expected Number=%d, got %d", i, i, tick.Number)
		}
		expectedPTS := int64(i) * 3000
		if tick.VideoPTS != expectedPTS {
			t.Fatalf("tick %d: expected PTS=%d, got %d", i, expectedPTS, tick.VideoPTS)
		}
		delta := tick.VideoPTS - prev
		if delta != 3000 {
			t.Fatalf("tick %d: expected delta=3000, got %d", i, delta)
		}
		if tick.VideoPTS <= prev && i > 0 {
			t.Fatalf("tick %d: PTS not monotonic: %d <= %d", i, tick.VideoPTS, prev)
		}
		prev = tick.VideoPTS
	}
}

func TestReferenceClockTickPTS2997(t *testing.T) {
	// 29.97fps = 30000/1001: PTS delta = 90000 * 1001 / 30000 = 3003
	c := NewReferenceClock(30000, 1001, 48000, 0, false)
	c.Start()
	defer c.Stop()

	for i := 0; i < 100; i++ {
		tick := <-c.Ticks()
		expectedPTS := int64(i) * 3003
		if tick.VideoPTS != expectedPTS {
			t.Fatalf("tick %d: expected PTS=%d, got %d", i, expectedPTS, tick.VideoPTS)
		}
	}
}

func TestReferenceClockAudioSamples(t *testing.T) {
	// 29.97fps, 48kHz. Over 30000 ticks (exactly 1001 seconds),
	// total samples must be 48000 * 1001 = 48,048,000.
	c := NewReferenceClock(30000, 1001, 48000, 0, false)
	c.Start()
	defer c.Stop()

	var totalSamples int64
	for i := 0; i < 30000; i++ {
		tick := <-c.Ticks()
		totalSamples += int64(tick.AudioSamples)
	}
	expected := int64(48000) * 1001 // 48,048,000
	if totalSamples != expected {
		t.Fatalf("expected total samples=%d, got %d", expected, totalSamples)
	}
}

func TestReferenceClockFrameDuration(t *testing.T) {
	tests := []struct {
		name   string
		fpsNum int
		fpsDen int
		want   time.Duration
	}{
		{
			name:   "29.97fps",
			fpsNum: 30000,
			fpsDen: 1001,
			want:   time.Duration(int64(1001) * int64(time.Second) / 30000), // ~33.37ms
		},
		{
			name:   "25fps",
			fpsNum: 25,
			fpsDen: 1,
			want:   40 * time.Millisecond,
		},
		{
			name:   "60fps",
			fpsNum: 60,
			fpsDen: 1,
			want:   time.Duration(int64(time.Second) / 60), // ~16.67ms
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewReferenceClock(tt.fpsNum, tt.fpsDen, 48000, 0, false)
			c.Start()
			defer c.Stop()

			tick := <-c.Ticks()
			if tick.FrameDuration != tt.want {
				t.Fatalf("expected FrameDuration=%v, got %v", tt.want, tick.FrameDuration)
			}
		})
	}
}

func TestReferenceClockStopClean(t *testing.T) {
	// Record goroutine count before.
	runtime.GC()
	before := runtime.NumGoroutine()

	c := NewReferenceClock(30, 1, 48000, 0, false)
	c.Start()

	// Consume a few ticks.
	for i := 0; i < 5; i++ {
		<-c.Ticks()
	}

	c.Stop()

	// Give the runtime a moment to clean up.
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	runtime.GC()

	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("goroutine leak: before=%d, after=%d", before, after)
	}
}

func TestReferenceClockDeterministic(t *testing.T) {
	// Non-realtime: produce 1000 ticks twice — same PTS sequence.
	const n = 1000

	run := func() []int64 {
		c := NewReferenceClock(30000, 1001, 48000, 0, false)
		c.Start()
		defer c.Stop()

		pts := make([]int64, n)
		for i := 0; i < n; i++ {
			tick := <-c.Ticks()
			pts[i] = tick.VideoPTS
		}
		return pts
	}

	seq1 := run()
	seq2 := run()

	for i := 0; i < n; i++ {
		if seq1[i] != seq2[i] {
			t.Fatalf("tick %d: run1 PTS=%d != run2 PTS=%d", i, seq1[i], seq2[i])
		}
	}
}

func TestReferenceClockRealtimePacing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping realtime pacing test in short mode")
	}

	c := NewReferenceClock(30, 1, 48000, 0, true)
	c.Start()
	defer c.Stop()

	start := time.Now()
	for i := 0; i < 30; i++ {
		<-c.Ticks()
	}
	elapsed := time.Since(start)

	// 30 ticks at 30fps should take ~1s (±150ms for scheduling jitter).
	if elapsed < 850*time.Millisecond || elapsed > 1150*time.Millisecond {
		t.Fatalf("expected ~1s for 30 ticks at 30fps, got %v", elapsed)
	}
}

// Issue 2: 59.94fps PTS truncation drift test.
// ptsDelta(60000, 1001) truncates 1501.5 to 1501.
// Over 60000 ticks the cumulative error is 30,000 ticks (333ms).
func TestPTSAccuracy5994fps(t *testing.T) {
	rc := NewReferenceClock(60000, 1001, 48000, 0, false)
	rc.Start()
	defer rc.Stop()

	const numTicks = 60000
	var totalPTSAdvance int64
	var prevPTS int64
	for i := 0; i < numTicks; i++ {
		tick := <-rc.Ticks()
		if i > 0 {
			totalPTSAdvance += tick.VideoPTS - prevPTS
		}
		prevPTS = tick.VideoPTS
	}

	// Exact total PTS advance after 59999 deltas at 59.94fps (60000/1001):
	// Each tick should advance by exactly 90000*1001/60000 = 1501.5 on average.
	// Total advance = 59999 * 90000 * 1001 / 60000 (Bresenham exact).
	// With truncation bug: 59999 * 1501 = 90,058,499 (short by ~30,000).
	// With Bresenham: last PTS = 59999 * 90000 * 1001 / 60000
	// Total sum of all deltas should be: lastPTS - firstPTS = lastPTS (since firstPTS=0).
	// With correct Bresenham over 60000 ticks:
	// tick 59999 PTS should be close to 59999 * 90000 * 1001 / 60000 = 90,088,498.5
	// The key test: the final PTS should not have drifted.
	// After exactly 60000 ticks (tick indices 0..59999), total span = tick[59999].PTS - tick[0].PTS = totalPTSAdvance
	// Expected span = 59999 * 90000 * 1001 / 60000
	// = 59999 * 90090000 / 60000
	// = 5,401,309,910,000 / 60000
	// = 90,021,831.833... (hmm, not integer)
	//
	// Actually the simplest check: after numTicks ticks, the PTS of the last tick
	// should match what you'd get from Bresenham accumulation.
	// Total Bresenham advance over 60000 deltas = 60000 * 90000 * 1001 / 60000 = 90000 * 1001 = 90,090,000.
	// So PTS of tick[59999] = sum of first 60000-1=59999 deltas from Bresenham,
	// but since the accumulator covers all 60000 deltas, that's 90,090,000 - last_delta.
	//
	// Easier: just check that the last PTS is in the right ballpark.
	// With truncation: lastPTS = 59999 * 1501 = 90,058,499
	// With Bresenham: lastPTS should be ~90,088,499 (approximately)
	// Difference = ~30,000 (333ms of drift)

	// The most robust check: lastPTS should NOT have 333ms of drift.
	expectedApprox := int64(59999) * int64(90000) * int64(1001) / int64(60000) // = 90,088,498 (integer division)
	drift := prevPTS - expectedApprox
	if drift < 0 {
		drift = -drift
	}
	// Allow up to 1502 ticks tolerance (one frame delta) for Bresenham rounding.
	if drift > 1502 {
		t.Fatalf("59.94fps PTS drift after %d ticks: lastPTS=%d, expected~%d, drift=%d (>1 frame)",
			numTicks, prevPTS, expectedApprox, drift)
	}
}

// Issue 3: Verify that audio samples from consumed ticks have valid Bresenham
// distribution even when ticks are dropped in realtime mode. Each consumed tick
// should have a sample count within the expected Bresenham range.
func TestRealtimeDroppedTickAudioIntegrity(t *testing.T) {
	// Use realtime clock at 29.97fps with a slow consumer to force drops.
	rc := NewReferenceClock(30000, 1001, 48000, 0, true)
	rc.Start()

	var consumedTicks int64
	var invalidSamples int
	deadline := time.After(500 * time.Millisecond)

	for {
		select {
		case tick, ok := <-rc.Ticks():
			if !ok {
				goto done
			}
			consumedTicks++
			// At 48000Hz/29.97fps, Bresenham produces 1601 or 1602 samples.
			if tick.AudioSamples != 1601 && tick.AudioSamples != 1602 {
				invalidSamples++
			}
			// Simulate slow consumer to force some drops.
			time.Sleep(50 * time.Millisecond)
		case <-deadline:
			goto done
		}
	}
done:
	rc.Stop()

	if consumedTicks == 0 {
		t.Fatal("no ticks consumed")
	}
	if invalidSamples > 0 {
		t.Fatalf("found %d ticks with invalid AudioSamples (expected 1601 or 1602)", invalidSamples)
	}
	t.Logf("consumed %d ticks (some dropped) - all had valid Bresenham sample counts", consumedTicks)
}

func TestReferenceClockBasePTS(t *testing.T) {
	t.Run("basePTS=90000", func(t *testing.T) {
		c := NewReferenceClock(30, 1, 48000, 90000, false)
		c.Start()
		defer c.Stop()

		tick := <-c.Ticks()
		if tick.VideoPTS != 90000 {
			t.Fatalf("expected first PTS=90000, got %d", tick.VideoPTS)
		}
	})

	t.Run("basePTS=0", func(t *testing.T) {
		c := NewReferenceClock(30, 1, 48000, 0, false)
		c.Start()
		defer c.Stop()

		tick := <-c.Ticks()
		if tick.VideoPTS != 0 {
			t.Fatalf("expected first PTS=0, got %d", tick.VideoPTS)
		}
	})
}
