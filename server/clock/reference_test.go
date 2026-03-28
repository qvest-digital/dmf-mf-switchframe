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

func TestReferenceClockSetRate(t *testing.T) {
	// Start at 30fps, consume 10 ticks.
	c := NewReferenceClock(30, 1, 48000, 0, false)
	c.Start()
	defer c.Stop()

	for i := 0; i < 10; i++ {
		<-c.Ticks()
	}

	// Switch to 25fps.
	c.SetRate(25, 1)

	// PTS delta should now be 90000/25 = 3600.
	// After SetRate, tickCount is 10, so next PTS = basePTS + 10 * 3600 = 36000.
	tick := <-c.Ticks()
	expectedPTS := int64(10) * 3600
	if tick.VideoPTS != expectedPTS {
		t.Fatalf("after SetRate: expected PTS=%d, got %d", expectedPTS, tick.VideoPTS)
	}

	// Verify consecutive delta is 3600.
	tick2 := <-c.Ticks()
	delta := tick2.VideoPTS - tick.VideoPTS
	if delta != 3600 {
		t.Fatalf("after SetRate: expected delta=3600, got %d", delta)
	}

	// AudioSamples at 25fps/48kHz = 48000/25 = 1920 exact.
	if tick.AudioSamples != 1920 {
		t.Fatalf("after SetRate: expected AudioSamples=1920, got %d", tick.AudioSamples)
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
