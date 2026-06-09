package playout

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// SecondaryEventHandler dispatches secondary events to the appropriate subsystem.
type SecondaryEventHandler interface {
	OnGraphicsOn(layer int, template string) error
	OnGraphicsOff(layer int) error
	OnGraphicsUpdate(layer int, template string) error
	OnAudioLevel(sourceKey string, levelDB float64) error
	OnAudioMute(sourceKey string, muted bool) error
	OnAudioAFV(sourceKey string, afv bool) error
	OnCaptionMode(mode string) error
	OnCaptionText(text string) error
	OnSCTE35Cue(data *SCTE35EventData) error
}

// scheduledEvent pairs a computed absolute fire time with the original event.
type scheduledEvent struct {
	fireMs int64
	event  SecondaryEvent
	fired  bool
}

// SecondaryEventScheduler manages timed secondary events for a playlist item.
//
// BeginItem, EndItem, Reanchor, and Tick are all driven by the owning Channel's
// single run goroutine and are not safe to call concurrently with each other.
// SetAsRunLog, however, may be invoked from a different goroutine (e.g. when the
// as-run logger is wired or re-wired via Channel.SetAsRunLog), so the asRunLog
// callback is guarded by mu to establish a happens-before relationship with the
// Tick reader and avoid a data race on reassignment.
type SecondaryEventScheduler struct {
	handler    SecondaryEventHandler
	events     []scheduledEvent
	durationMs int64

	mu       sync.Mutex // guards asRunLog
	asRunLog func(AsRunEvent)
}

// NewSecondaryEventScheduler creates a scheduler that dispatches to the given handler.
func NewSecondaryEventScheduler(handler SecondaryEventHandler) *SecondaryEventScheduler {
	return &SecondaryEventScheduler{
		handler: handler,
	}
}

// SetAsRunLog registers a callback for emitting as-run events when secondary
// events fire. Safe to call concurrently with Tick.
func (s *SecondaryEventScheduler) SetAsRunLog(fn func(AsRunEvent)) {
	s.mu.Lock()
	s.asRunLog = fn
	s.mu.Unlock()
}

// getAsRunLog returns the current as-run callback under the lock.
func (s *SecondaryEventScheduler) getAsRunLog() func(AsRunEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asRunLog
}

// BeginItem loads secondary events for a new playlist item, computing absolute
// fire times relative to the item's duration.
func (s *SecondaryEventScheduler) BeginItem(events []SecondaryEvent, durationMs int64) {
	s.durationMs = durationMs
	s.events = make([]scheduledEvent, len(events))
	for i, ev := range events {
		s.events[i] = scheduledEvent{
			fireMs: resolveFireTime(ev, durationMs),
			event:  ev,
		}
	}
	sort.Slice(s.events, func(i, j int) bool {
		return s.events[i].fireMs < s.events[j].fireMs
	})
}

// EndItem clears the current schedule.
func (s *SecondaryEventScheduler) EndItem() {
	s.events = nil
	s.durationMs = 0
}

// Tick checks whether any scheduled events should fire at the given elapsed time.
// Events are fired in order and marked to prevent re-firing.
func (s *SecondaryEventScheduler) Tick(elapsedMs int64) {
	asRunLog := s.getAsRunLog()
	for i := range s.events {
		if s.events[i].fired {
			continue
		}
		if s.events[i].fireMs > elapsedMs {
			break // events are sorted, no more can fire
		}
		s.events[i].fired = true
		if err := s.dispatch(&s.events[i].event); err != nil {
			slog.Warn("playout: secondary event dispatch failed", "type", s.events[i].event.Type, "error", err)
		}
		if asRunLog != nil {
			asRunLog(AsRunEvent{
				Timestamp: time.Now().UTC(),
				Type:      AsRunSecondaryFire,
				EventName: s.events[i].event.Type,
			})
		}
	}
}

// Reanchor recalculates fire times for end-anchored events when the item
// duration changes (e.g., live segment extended/shortened). Start-anchored
// events keep their original fire times. Already-fired events are not reset.
func (s *SecondaryEventScheduler) Reanchor(newDurationMs int64) {
	s.durationMs = newDurationMs
	for i := range s.events {
		if s.events[i].event.Anchor == "end" {
			s.events[i].fireMs = resolveFireTime(s.events[i].event, newDurationMs)
			// Do not reset the fired flag — already-fired events stay fired.
		}
	}
	sort.Slice(s.events, func(i, j int) bool {
		return s.events[i].fireMs < s.events[j].fireMs
	})
}

// fireTimeUnknown marks an end-anchored event whose item duration is not yet
// known. The event is held (sorted last, never reached by Tick) until Reanchor
// supplies a real duration. Without this, an unknown (zero) duration would
// clamp the fire time to 0 and incorrectly fire the event at item start.
const fireTimeUnknown = int64(math.MaxInt64)

// resolveFireTime computes the absolute fire time in milliseconds.
// Start-anchored: offsetMs from start. End-anchored: durationMs + offsetMs.
// Clamped to 0 minimum. End-anchored events with no known duration are
// deferred (fireTimeUnknown) until Reanchor is called with a real duration.
func resolveFireTime(ev SecondaryEvent, durationMs int64) int64 {
	var t int64
	switch ev.Anchor {
	case "end":
		if durationMs <= 0 {
			return fireTimeUnknown
		}
		t = durationMs + int64(ev.OffsetMs)
	default: // "start"
		t = int64(ev.OffsetMs)
	}
	if t < 0 {
		t = 0
	}
	return t
}

// dispatch routes a secondary event to the appropriate handler method.
func (s *SecondaryEventScheduler) dispatch(ev *SecondaryEvent) error {
	switch ev.Type {
	case "graphics_on":
		return s.handler.OnGraphicsOn(ev.Layer, ev.Template)
	case "graphics_off":
		return s.handler.OnGraphicsOff(ev.Layer)
	case "graphics_update":
		return s.handler.OnGraphicsUpdate(ev.Layer, ev.Template)
	case "audio_level":
		levelDB := 0.0
		if ev.LevelDB != nil {
			levelDB = *ev.LevelDB
		}
		return s.handler.OnAudioLevel(ev.SourceKey, levelDB)
	case "audio_mute":
		muted := false
		if ev.Muted != nil {
			muted = *ev.Muted
		}
		return s.handler.OnAudioMute(ev.SourceKey, muted)
	case "audio_afv":
		afv := false
		if ev.AFV != nil {
			afv = *ev.AFV
		}
		return s.handler.OnAudioAFV(ev.SourceKey, afv)
	case "caption_mode":
		return s.handler.OnCaptionMode(ev.CaptionMode)
	case "caption_text":
		return s.handler.OnCaptionText(ev.CaptionText)
	case "scte35_cue":
		return s.handler.OnSCTE35Cue(ev.SCTE35)
	default:
		return fmt.Errorf("secondary: unknown event type %q", ev.Type)
	}
}
