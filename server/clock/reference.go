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

// ReferenceClock produces ticks at a stable frame rate, driving synchronous
// A/V processing. Rate can be changed at runtime (for source clock recovery).
type ReferenceClock struct {
	mu         sync.Mutex
	fpsNum     int
	fpsDen     int
	sampleRate int
	accum      *AudioAccumulator
	tickCount  int64
	basePTS    int64
	ticks      chan Tick
	request    chan struct{} // non-realtime: consumer signals readiness
	stop       chan struct{}
	wg         sync.WaitGroup
	realtime   bool
	running    atomic.Bool
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
		basePTS:    basePTS,
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
func (c *ReferenceClock) Stop() {
	if !c.running.Swap(false) {
		return // not running
	}
	close(c.stop)
	c.wg.Wait()
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
func (c *ReferenceClock) SetRate(fpsNum, fpsDen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	return atomic.LoadInt64(&c.tickCount)
}

// frameDuration returns the nominal frame duration for the given rate.
func frameDuration(fpsNum, fpsDen int) time.Duration {
	// duration = fpsDen / fpsNum seconds
	// In nanoseconds: fpsDen * 1e9 / fpsNum
	return time.Duration(int64(fpsDen) * int64(time.Second) / int64(fpsNum))
}

// ptsDelta returns the 90kHz PTS increment per frame for the given rate.
func ptsDelta(fpsNum, fpsDen int) int64 {
	return int64(90000) * int64(fpsDen) / int64(fpsNum)
}

// produceTick creates the next tick under the lock, advancing state.
func (c *ReferenceClock) produceTick() Tick {
	c.mu.Lock()
	num := c.fpsNum
	den := c.fpsDen
	dur := frameDuration(num, den)
	delta := ptsDelta(num, den)
	samples := c.accum.SamplesForTick()
	count := c.tickCount
	pts := c.basePTS + count*delta
	c.tickCount++
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
