package playout

import (
	"context"
	"sort"
	"sync"
	"time"
)

// WatchdogConfig configures the playout watchdog.
type WatchdogConfig struct {
	StallThreshold time.Duration
	CheckInterval  time.Duration
	OnStall        func(channelID int)
}

// Watchdog monitors playout channel frame output cadence and fires OnStall
// callbacks when no frames arrive within StallThreshold.
type Watchdog struct {
	config   WatchdogConfig
	mu       sync.Mutex
	channels map[int]*watchdogChannel
	cancel   context.CancelFunc
	done     chan struct{}
}

type watchdogChannel struct {
	lastFrame time.Time
	stalled   bool
}

// ChannelHealth reports the health of a single monitored channel.
type ChannelHealth struct {
	ChannelID   int   `json:"channel_id"`
	Healthy     bool  `json:"healthy"`
	LastFrameMs int64 `json:"last_frame_ms"`
	Stalled     bool  `json:"stalled"`
}

// NewWatchdog creates a Watchdog with the given config, applying defaults
// for zero-valued StallThreshold (5s) and CheckInterval (1s).
func NewWatchdog(config WatchdogConfig) *Watchdog {
	if config.StallThreshold == 0 {
		config.StallThreshold = 5 * time.Second
	}
	if config.CheckInterval == 0 {
		config.CheckInterval = 1 * time.Second
	}
	return &Watchdog{
		config:   config,
		channels: make(map[int]*watchdogChannel),
	}
}

// RegisterChannel starts monitoring the given channel, setting lastFrame to now.
func (w *Watchdog) RegisterChannel(channelID int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.channels[channelID] = &watchdogChannel{
		lastFrame: time.Now(),
	}
}

// UnregisterChannel stops monitoring the given channel.
func (w *Watchdog) UnregisterChannel(channelID int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.channels, channelID)
}

// ReportFrame updates the last frame timestamp for the given channel and
// resets its stalled flag. Unregistered channels are silently ignored.
func (w *Watchdog) ReportFrame(channelID int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, ok := w.channels[channelID]
	if !ok {
		return
	}
	ch.lastFrame = time.Now()
	ch.stalled = false
}

// Start begins the background check goroutine.
func (w *Watchdog) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.run(ctx)
}

// Stop halts the background check goroutine and waits for it to exit.
func (w *Watchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.done != nil {
		<-w.done
		w.done = nil
	}
}

// Health returns the health status of all registered channels.
func (w *Watchdog) Health() []ChannelHealth {
	w.mu.Lock()
	defer w.mu.Unlock()

	result := make([]ChannelHealth, 0, len(w.channels))
	for id, ch := range w.channels {
		result = append(result, ChannelHealth{
			ChannelID:   id,
			Healthy:     !ch.stalled,
			LastFrameMs: time.Since(ch.lastFrame).Milliseconds(),
			Stalled:     ch.stalled,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ChannelID < result[j].ChannelID
	})
	return result
}

func (w *Watchdog) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

// check examines all registered channels and fires OnStall for newly stalled ones.
func (w *Watchdog) check() {
	now := time.Now()
	var stalledIDs []int

	w.mu.Lock()
	for id, ch := range w.channels {
		if now.Sub(ch.lastFrame) > w.config.StallThreshold && !ch.stalled {
			ch.stalled = true
			stalledIDs = append(stalledIDs, id)
		}
	}
	w.mu.Unlock()

	// Fire callbacks outside the lock.
	if w.config.OnStall != nil {
		for _, id := range stalledIDs {
			w.config.OnStall(id)
		}
	}
}
