package scte35

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

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
type WebhookDispatcher struct {
	url    string
	client *http.Client
}

// NewWebhookDispatcher creates a webhook dispatcher. If url is empty, Dispatch is a no-op.
func NewWebhookDispatcher(url string, timeout time.Duration) *WebhookDispatcher {
	return &WebhookDispatcher{
		url: url,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Dispatch sends a webhook event asynchronously. Never blocks the caller.
// Errors are logged but not returned.
func (w *WebhookDispatcher) Dispatch(event WebhookEvent) {
	if w.url == "" {
		return
	}

	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	go func() {
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
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			slog.Warn("webhook returned error", "url", w.url, "status", resp.StatusCode)
		}
	}()
}
