package scte35

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// webhookQueueSize is the bounded dispatch queue capacity.
// Events are dropped if the queue is full (slow webhook endpoint).
const webhookQueueSize = 64

// WebhookEvent is the JSON payload sent to external webhook endpoints.
type WebhookEvent struct {
	Type      string `json:"type"`                    // "cue_out", "cue_in", "cancel", "hold", "extend", "heartbeat"
	EventID   uint32 `json:"eventId"`
	Command   string `json:"command"`                 // "splice_insert", "time_signal", "splice_null"
	IsOut     bool   `json:"isOut"`
	Duration  int64  `json:"durationMs,omitempty"`
	Remaining int64  `json:"remainingMs,omitempty"`
	Timestamp int64  `json:"timestamp"`
	PTS       int64  `json:"pts,omitempty"`
}

// WebhookDispatcher sends webhook events to an external URL.
// Uses a bounded channel and single worker goroutine to prevent
// unbounded goroutine spawning under high event rates.
type WebhookDispatcher struct {
	url       string
	client    *http.Client
	queue     chan WebhookEvent
	done      chan struct{}
	closed    atomic.Bool
	closeOnce sync.Once
}

// NewWebhookDispatcher creates a webhook dispatcher. If url is empty, Dispatch is a no-op.
func NewWebhookDispatcher(url string, timeout time.Duration) *WebhookDispatcher {
	w := &WebhookDispatcher{
		url: url,
		client: &http.Client{
			Timeout: timeout,
		},
		queue: make(chan WebhookEvent, webhookQueueSize),
		done:  make(chan struct{}),
	}
	go w.worker()
	return w
}

// Dispatch queues a webhook event for async delivery. Never blocks the caller.
// If the queue is full or the dispatcher is closed, the event is dropped.
func (w *WebhookDispatcher) Dispatch(event WebhookEvent) {
	if w.url == "" || w.closed.Load() {
		return
	}

	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	select {
	case w.queue <- event:
	default:
		slog.Warn("webhook queue full, dropping event", "type", event.Type, "eventId", event.EventID)
	}
}

// Close drains the queue and stops the worker goroutine. Safe to call multiple times.
func (w *WebhookDispatcher) Close() {
	w.closed.Store(true)
	w.closeOnce.Do(func() {
		close(w.queue)
	})
	<-w.done
}

// worker drains the dispatch queue, sending each event via HTTP POST.
func (w *WebhookDispatcher) worker() {
	defer close(w.done)
	for event := range w.queue {
		w.send(event)
	}
}

// send performs the HTTP POST for a single event.
func (w *WebhookDispatcher) send(event WebhookEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		slog.Error("webhook marshal failed", "error", err)
		return
	}

	resp, err := w.client.Post(w.url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Debug("webhook dispatch failed", "url", w.url, "error", err)
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("webhook returned error", "url", w.url, "status", resp.StatusCode)
	}
}
