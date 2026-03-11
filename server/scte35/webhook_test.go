package scte35

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebhook_Dispatch(t *testing.T) {
	t.Parallel()
	var received WebhookEvent
	var mu sync.Mutex
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
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
		require.Fail(t, "webhook not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, uint32(42), received.EventID)
	require.Equal(t, "cue_out", received.Type)
}

func TestWebhook_Timeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow server
	}))
	defer srv.Close()

	wh := NewWebhookDispatcher(srv.URL, 100*time.Millisecond)
	defer wh.Close()

	start := time.Now()
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	// Dispatch is async — should return immediately
	require.Less(t, time.Since(start), 50*time.Millisecond, "Dispatch should not block caller")

	// Wait a bit for the worker to run and timeout
	time.Sleep(200 * time.Millisecond)
}

func TestWebhook_ServerDown(t *testing.T) {
	t.Parallel()
	wh := NewWebhookDispatcher("http://127.0.0.1:1", 100*time.Millisecond)
	defer wh.Close()

	// Should not panic or return error
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	time.Sleep(200 * time.Millisecond) // let worker run
}

func TestWebhook_Disabled(t *testing.T) {
	t.Parallel()
	wh := NewWebhookDispatcher("", 2*time.Second)
	defer wh.Close()

	// Should be a no-op
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
	// No panic, no HTTP calls
}

func TestWebhook_Close_Drains(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, 5, count)
}

func TestWebhook_Backpressure_DropsWhenFull(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	wh := NewWebhookDispatcher("http://127.0.0.1:1", 100*time.Millisecond)

	// Multiple Close() calls should not panic.
	wh.Close()
	wh.Close()
	wh.Close()
}
