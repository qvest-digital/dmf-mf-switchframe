package backoff

import (
	"context"
	"math/rand/v2"
	"time"
)

// Backoff implements exponential backoff with configurable jitter.
// Zero value uses sensible defaults: 1s initial, 30s max, no jitter.
type Backoff struct {
	Initial   time.Duration // default 1s
	Max       time.Duration // default 30s
	JitterPct float64       // 0.25 = +/-25%, 0 = no jitter

	current time.Duration
}

func (b *Backoff) init() {
	if b.Initial == 0 {
		b.Initial = time.Second
	}
	if b.Max == 0 {
		b.Max = 30 * time.Second
	}
	if b.current == 0 {
		b.current = b.Initial
	}
}

// Next returns the current backoff duration (with jitter applied) and
// advances to the next level. Thread-unsafe — callers must synchronize.
func (b *Backoff) Next() time.Duration {
	b.init()
	current := b.current

	next := current * 2
	if next > b.Max {
		next = b.Max
	}
	b.current = next

	return b.jitter(current)
}

// Reset returns the backoff to its initial value.
func (b *Backoff) Reset() {
	b.init()
	b.current = b.Initial
}

// Wait sleeps for the current backoff duration, respecting context cancellation.
// Returns ctx.Err() if the context is cancelled before the backoff elapses.
// Advances the backoff level (like Next).
func (b *Backoff) Wait(ctx context.Context) error {
	d := b.Next()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (b *Backoff) jitter(d time.Duration) time.Duration {
	if b.JitterPct <= 0 {
		return d
	}
	factor := 1.0 - b.JitterPct + rand.Float64()*2*b.JitterPct
	return time.Duration(float64(d) * factor)
}
