package clock

import (
	"sync"
	"sync/atomic"
	"time"
)

// Tick represents one frame period from the reference clock.
type Tick struct {
	Number        int64         // monotonic tick index
	VideoPTS      int64         // 90kHz PTS for this video frame
	AudioSamples  int           // exact sample count for this frame period
	FrameDuration time.Duration // nominal frame duration (for overload detection)
}

// ptsMask33 masks a PTS value to 33 bits (MPEG-TS PTS range: 0 to 2^33-1).
const ptsMask33 = int64(1<<33) - 1

// ReferenceClock produces ticks at a stable frame rate, driving synchronous
// A/V processing. Rate can be changed at runtime (for source clock recovery).
type ReferenceClock struct {
	mu           sync.Mutex
	fpsNum       int
	fpsDen       int
	sampleRate   int
	accum        *AudioAccumulator
	tickCount    atomic.Int64
	ticks        chan Tick
	request      chan struct{} // non-realtime: consumer signals readiness
	stop         chan struct{}
	wg           sync.WaitGroup
	realtime     bool
	running      atomic.Bool
	droppedTicks atomic.Int64 // ticks dropped because consumer was behind

	// currentPTS tracks the running PTS value using Bresenham accumulation
	// (Issue 2). Eliminates truncation drift for non-integer PTS deltas
	// (e.g., 59.94fps where exact delta = 1501.5).
	currentPTS int64

	// ptsRemainder is the Bresenham fractional remainder for PTS computation.
	ptsRemainder int64
}

// NewReferenceClock creates a clock at the given rate.
// realtime=true: ticks paced to wall clock (production).
// realtime=false: ticks produced when consumed (deterministic testing).
func NewReferenceClock(fpsNum, fpsDen, sampleRate int, basePTS int64, realtime bool) *ReferenceClock {
	c := &ReferenceClock{
		fpsNum:     fpsNum,
		fpsDen:     fpsDen,
		sampleRate: sampleRate,
		accum:      NewAudioAccumulator(sampleRate, fpsNum, fpsDen),
		currentPTS: basePTS,
		stop:       make(chan struct{}),
		realtime:   realtime,
	}
	if realtime {
		c.ticks = make(chan Tick, 1)
	} else {
		// Unbuffered: goroutine produces only after consumer signals readiness.
		c.ticks = make(chan Tick)
		c.request = make(chan struct{}, 1)
	}
	return c
}

// Start begins producing ticks on the Ticks channel.
func (c *ReferenceClock) Start() {
	if c.running.Swap(true) {
		return // already running
	}
	c.wg.Add(1)
	if c.realtime {
		go c.runRealtime()
	} else {
		go c.runNonRealtime()
	}
}

// Stop signals the clock to stop and waits for goroutine cleanup.
// Closes the Ticks channel after the producer goroutine exits, allowing
// consumers using range-over-channel to terminate cleanly.
func (c *ReferenceClock) Stop() {
	if !c.running.Swap(false) {
		return // not running
	}
	close(c.stop)
	c.wg.Wait()
	close(c.ticks)
}

// Ticks returns the receive channel for clock ticks.
// In non-realtime mode, each receive triggers computation of the next tick,
// ensuring SetRate takes effect immediately on the very next read.
func (c *ReferenceClock) Ticks() <-chan Tick {
	if !c.realtime {
		// Signal the producer to compute and send the next tick.
		// Non-blocking: if a request is already pending, don't block.
		select {
		case c.request <- struct{}{}:
		default:
		}
	}
	return c.ticks
}

// SetRate changes the clock rate. Thread-safe. Takes effect on next tick.
// Rebases the PTS accumulator so the next tick continues from the current
// PTS position, avoiding discontinuities (Issue 6).
func (c *ReferenceClock) SetRate(fpsNum, fpsDen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// currentPTS already holds the PTS for the next tick. Just reset the
	// Bresenham remainder so the new rate starts clean from currentPTS.
	c.ptsRemainder = 0
	c.fpsNum = fpsNum
	c.fpsDen = fpsDen
	c.accum.Reset(c.sampleRate, fpsNum, fpsDen)
}

// Rate returns the current rate.
func (c *ReferenceClock) Rate() (fpsNum, fpsDen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fpsNum, c.fpsDen
}

// TickCount returns ticks produced so far.
func (c *ReferenceClock) TickCount() int64 {
	return c.tickCount.Load()
}

// DroppedTicks returns the number of ticks dropped because the consumer
// couldn't keep up with the clock rate. Non-zero indicates overload.
func (c *ReferenceClock) DroppedTicks() int64 {
	return c.droppedTicks.Load()
}

// frameDuration returns the nominal frame duration for the given rate.
func frameDuration(fpsNum, fpsDen int) time.Duration {
	// duration = fpsDen / fpsNum seconds
	// In nanoseconds: fpsDen * 1e9 / fpsNum
	return time.Duration(int64(fpsDen) * int64(time.Second) / int64(fpsNum))
}

// produceTick creates the next tick under the lock, advancing state.
// Uses Bresenham accumulation for PTS (Issue 2) and 33-bit masking (Issue 8).
func (c *ReferenceClock) produceTick() Tick {
	c.mu.Lock()
	dur := frameDuration(c.fpsNum, c.fpsDen)
	samples := c.accum.SamplesForTick()
	count := c.tickCount.Load()

	// The PTS for this tick is currentPTS (already computed).
	pts := c.currentPTS & ptsMask33

	// Advance currentPTS for the NEXT tick using Bresenham accumulation.
	// This distributes the fractional PTS delta (e.g., 1501.5 for 59.94fps)
	// across ticks with zero cumulative error.
	c.ptsRemainder += int64(90000) * int64(c.fpsDen)
	delta := c.ptsRemainder / int64(c.fpsNum)
	c.ptsRemainder %= int64(c.fpsNum)
	c.currentPTS += delta

	c.tickCount.Add(1)
	c.mu.Unlock()

	return Tick{
		Number:        count,
		VideoPTS:      pts,
		AudioSamples:  samples,
		FrameDuration: dur,
	}
}

// runRealtime produces wall-clock-paced ticks with drift compensation.
func (c *ReferenceClock) runRealtime() {
	defer c.wg.Done()

	next := time.Now()
	for {
		tick := c.produceTick()

		// Advance next deadline by nominal frame duration.
		// This compensates for late wakeups: if we wake 5ms late,
		// the next sleep is 5ms shorter.
		next = next.Add(tick.FrameDuration)

		select {
		case <-c.stop:
			return
		default:
		}

		// Non-blocking send: drop tick if consumer is behind (overload).
		select {
		case c.ticks <- tick:
		default:
			c.droppedTicks.Add(1)
		}

		// Sleep until next deadline.
		delay := time.Until(next)
		if delay <= 0 {
			// Already behind — don't sleep, continue immediately.
			continue
		}
		select {
		case <-c.stop:
			return
		case <-time.After(delay):
		}
	}
}

// runNonRealtime produces ticks only when the consumer requests them.
// No wall-clock dependency — deterministic for testing.
// The consumer calls Ticks() which sends a request, then reads the channel.
// This ensures SetRate takes effect before the tick is computed.
func (c *ReferenceClock) runNonRealtime() {
	defer c.wg.Done()

	for {
		// Wait for consumer to request a tick.
		select {
		case <-c.request:
		case <-c.stop:
			return
		}

		// Compute the tick now (after SetRate has had a chance to run).
		tick := c.produceTick()

		select {
		case c.ticks <- tick:
		case <-c.stop:
			return
		}
	}
}
