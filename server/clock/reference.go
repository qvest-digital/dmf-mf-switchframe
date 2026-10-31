package clock

import (
	"sync"
	"sync/atomic"
	"time"
)

// Tick is one program frame period. Index is the program grain index on
// TAI; every timestamp in the tick is derived from it.
type Tick struct {
	Number        int64         // ticks since the clock started
	Index         uint64        // program grain index (synthetic in non-realtime mode)
	VideoPTS      int64         // 90 kHz PTS of the grain's TAI
	ReadPTS       int64         // newest input media time this tick may show
	AudioStart    uint64        // first sample index of this period: I(Ra, T(Rv, Index))
	AudioSamples  int           // I(Ra, T(Rv, Index+1)) - AudioStart
	FrameDuration time.Duration // nominal frame duration (for overload detection)
}

// ReferenceClock produces one tick per program frame. In realtime mode the
// ticks are the grains of the program rate on TAI (ReaderDelayGrains
// behind is what each tick reads); otherwise they are produced on demand
// from index 0, for deterministic tests.
type ReferenceClock struct {
	rate         Rate
	sampleRate   Rate
	basePTS      int64
	tickCount    atomic.Int64
	ticks        chan Tick
	request      chan struct{} // non-realtime: consumer signals readiness
	stop         chan struct{}
	wg           sync.WaitGroup
	realtime     bool
	running      atomic.Bool
	droppedTicks atomic.Int64 // ticks not delivered because the consumer was behind
}

// NewReferenceClock creates a clock at the given rate.
// realtime=true: ticks are the program grains on TAI (production); basePTS
// is unused, PTS is the engine timeline's (PTSBase at Epoch).
// realtime=false: ticks produced when consumed, PTS from basePTS
// (deterministic testing).
func NewReferenceClock(fpsNum, fpsDen, sampleRate int, basePTS int64, realtime bool) *ReferenceClock {
	c := &ReferenceClock{
		rate:       Rate{int64(fpsNum), int64(fpsDen)},
		sampleRate: Rate{int64(sampleRate), 1},
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

// tickAt builds the tick for program grain index n.
func (c *ReferenceClock) tickAt(n uint64) Tick {
	ts, next, read := c.timestamps(n)
	start := TimestampToIndex(c.sampleRate, ts)
	return Tick{
		Number:        c.tickCount.Add(1) - 1,
		Index:         n,
		VideoPTS:      c.pts(ts),
		ReadPTS:       c.pts(read),
		AudioStart:    start,
		AudioSamples:  int(TimestampToIndex(c.sampleRate, next) - start),
		FrameDuration: frameDuration(int(c.rate.Num), int(c.rate.Den)),
	}
}

// timestamps returns the TAI of grain n, of grain n+1 and of the grain the
// tick reads, n - ReaderDelayGrains. Non-realtime timestamps are offset so
// that index 0 is time 0 and the read grain of the first ticks exists.
func (c *ReferenceClock) timestamps(n uint64) (ts, next, read int64) {
	if c.realtime {
		return IndexToTimestamp(c.rate, n), IndexToTimestamp(c.rate, n+1),
			IndexToTimestamp(c.rate, n-ReaderDelayGrains)
	}
	off := IndexToTimestamp(c.rate, ReaderDelayGrains)
	s := n + ReaderDelayGrains
	return IndexToTimestamp(c.rate, s) - off, IndexToTimestamp(c.rate, s+1) - off,
		IndexToTimestamp(c.rate, n) - off
}

func (c *ReferenceClock) pts(ts int64) int64 {
	if c.realtime {
		return PTS(ts)
	}
	return c.basePTS + roundDiv(ts*9, 100_000)
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
// In non-realtime mode, each receive triggers computation of the next tick.
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

// Rate returns the program frame rate.
func (c *ReferenceClock) Rate() (fpsNum, fpsDen int) {
	return int(c.rate.Num), int(c.rate.Den)
}

// catchUp returns the grain to deliver at TAI now when grain n is next:
// n itself while now is still within it, otherwise the grain now is in,
// counting the grains that passed undelivered.
func (c *ReferenceClock) catchUp(n uint64, now int64) uint64 {
	if now < IndexToTimestamp(c.rate, n+1) {
		return n
	}
	cur := n + 1
	for now >= IndexToTimestamp(c.rate, cur+1) {
		cur++
	}
	c.droppedTicks.Add(int64(cur - n))
	return cur
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

// runRealtime delivers one tick at the TAI start of each program grain.
// A tick the consumer has not taken by the next one is dropped; after a
// stall the clock resumes at the current grain, so a tick's PTS is always
// its grain's.
func (c *ReferenceClock) runRealtime() {
	defer c.wg.Done()

	n := TimestampToIndex(c.rate, TAINow()) + 1
	for {
		if wait := IndexToTimestamp(c.rate, n) - TAINow(); wait > 0 {
			select {
			case <-c.stop:
				return
			case <-time.After(time.Duration(wait)):
			}
		}
		select {
		case <-c.stop:
			return
		default:
		}
		n = c.catchUp(n, TAINow())
		select {
		case c.ticks <- c.tickAt(n):
		default:
			c.droppedTicks.Add(1)
		}
		n++
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

		tick := c.tickAt(uint64(c.tickCount.Load()))

		select {
		case c.ticks <- tick:
		case <-c.stop:
			return
		}
	}
}
