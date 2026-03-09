package scte35

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestWebhook_Dispatch(t *testing.T) {
	var received WebhookEvent
	var mu sync.Mutex
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type")
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&received)
		close(done)
	}))
	defer srv.Close()

	wh := NewWebhookDispatcher(srv.URL, 2*time.Second)
	defer wh.Close()
	wh.Dispatch(WebhookEvent{
		Type:    "cue_out",
		EventID: 42,
		Command: "splice_insert",
		IsOut:   true,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if received.EventID != 42 {
		t.Fatalf("expected event ID 42, got %d", received.EventID)
	}
	if received.Type != "cue_out" {
		t.Fatalf("expected type cue_out, got %s", received.Type)
	}
}

func TestWebhook_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow server
	}))
	defer srv.Close()

	wh := NewWebhookDispatcher(srv.URL, 100*time.Millisecond)
	defer wh.Close()

	start := time.Now()
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	// Dispatch is async — should return immediately
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("Dispatch should not block caller")
	}

	// Wait a bit for the worker to run and timeout
	time.Sleep(200 * time.Millisecond)
}

func TestWebhook_ServerDown(t *testing.T) {
	wh := NewWebhookDispatcher("http://127.0.0.1:1", 100*time.Millisecond)
	defer wh.Close()

	// Should not panic or return error
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	time.Sleep(200 * time.Millisecond) // let worker run
}

func TestWebhook_Disabled(t *testing.T) {
	wh := NewWebhookDispatcher("", 2*time.Second)
	defer wh.Close()

	// Should be a no-op
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	// No panic, no HTTP calls
}

func TestWebhook_Close_Drains(t *testing.T) {
	var mu sync.Mutex
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
	}))
	defer srv.Close()

	wh := NewWebhookDispatcher(srv.URL, 2*time.Second)
	for i := 0; i < 5; i++ {
		wh.Dispatch(WebhookEvent{Type: "test", EventID: uint32(i)})
	}
	wh.Close()

	mu.Lock()
	defer mu.Unlock()
	if count != 5 {
		t.Fatalf("expected 5 events delivered after Close, got %d", count)
	}
}

func TestWebhook_Backpressure_DropsWhenFull(t *testing.T) {
	// Block the worker so the queue fills up.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()

	wh := NewWebhookDispatcher(srv.URL, 2*time.Second)

	// Fill the queue past capacity. webhookQueueSize is 64.
	for i := 0; i < webhookQueueSize+10; i++ {
		wh.Dispatch(WebhookEvent{Type: "test", EventID: uint32(i)})
	}

	// Should not have panicked or deadlocked. Unblock and close.
	close(block)
	wh.Close()
}

func TestWebhook_Close_Idempotent(t *testing.T) {
	wh := NewWebhookDispatcher("http://127.0.0.1:1", 100*time.Millisecond)

	// Multiple Close() calls should not panic.
	wh.Close()
	wh.Close()
	wh.Close()
}
