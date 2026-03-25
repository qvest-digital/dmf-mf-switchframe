package nmos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegisterResource(t *testing.T) {
	var receivedBody map[string]json.RawMessage
	var receivedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/x-nmos/registration/v1.3/resource" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		receivedContentType = r.Header.Get("Content-Type")

		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")
	node := &Node{
		ID:    "node-123",
		Label: "Test Node",
	}

	err := client.RegisterResource(context.Background(), "node", node)
	if err != nil {
		t.Fatalf("RegisterResource returned error: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", receivedContentType)
	}

	// Verify "type" field.
	var resourceType string
	if err := json.Unmarshal(receivedBody["type"], &resourceType); err != nil {
		t.Fatalf("failed to unmarshal type field: %v", err)
	}
	if resourceType != "node" {
		t.Errorf("expected type 'node', got %q", resourceType)
	}

	// Verify "data" field contains the node.
	var data Node
	if err := json.Unmarshal(receivedBody["data"], &data); err != nil {
		t.Fatalf("failed to unmarshal data field: %v", err)
	}
	if data.ID != "node-123" {
		t.Errorf("expected node ID 'node-123', got %q", data.ID)
	}
	if data.Label != "Test Node" {
		t.Errorf("expected label 'Test Node', got %q", data.Label)
	}
}

func TestRegisterResourceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "registry unavailable")
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")
	err := client.RegisterResource(context.Background(), "node", &Node{ID: "node-123"})
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}

	// Error should include status code.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "500") {
		t.Errorf("error should contain status code 500: %s", errMsg)
	}
	// Error should include response body.
	if !strings.Contains(errMsg, "registry unavailable") {
		t.Errorf("error should contain response body: %s", errMsg)
	}
}

func TestRegisterAll(t *testing.T) {
	var mu sync.Mutex
	var registrationOrder []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}

		var resourceType string
		if err := json.Unmarshal(body["type"], &resourceType); err != nil {
			t.Fatalf("failed to unmarshal type: %v", err)
		}

		mu.Lock()
		registrationOrder = append(registrationOrder, resourceType)
		mu.Unlock()

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")

	node := &Node{ID: "node-1"}
	device := &Device{ID: "device-1"}
	sources := []*Source{{ID: "src-1"}, {ID: "src-2"}}
	flows := []*Flow{{ID: "flow-1"}}
	senders := []*Sender{{ID: "sender-1"}}
	receivers := []*Receiver{{ID: "receiver-1"}}

	err := client.RegisterAll(context.Background(), node, device, sources, flows, senders, receivers)
	if err != nil {
		t.Fatalf("RegisterAll returned error: %v", err)
	}

	// Expected order: node, device, src-1, src-2, flow-1, sender-1, receiver-1
	expected := []string{"node", "device", "source", "source", "flow", "sender", "receiver"}
	if len(registrationOrder) != len(expected) {
		t.Fatalf("expected %d registrations, got %d: %v", len(expected), len(registrationOrder), registrationOrder)
	}
	for i, exp := range expected {
		if registrationOrder[i] != exp {
			t.Errorf("registration[%d]: expected %q, got %q (full order: %v)", i, exp, registrationOrder[i], registrationOrder)
		}
	}
}

func TestRegisterAllStopsOnError(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 2 {
			// Fail on the device registration.
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "device error")
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")
	err := client.RegisterAll(context.Background(),
		&Node{ID: "n"}, &Device{ID: "d"},
		[]*Source{{ID: "s"}}, []*Flow{{ID: "f"}},
		[]*Sender{{ID: "snd"}}, []*Receiver{{ID: "rcv"}},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should have stopped after the device failure (2 calls total).
	if callCount != 2 {
		t.Errorf("expected 2 calls before stopping, got %d", callCount)
	}
}

func TestHeartbeat(t *testing.T) {
	var receivedPath string
	var receivedMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-abc-123")
	err := client.Heartbeat(context.Background())
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	expectedPath := "/x-nmos/registration/v1.3/health/nodes/node-abc-123"
	if receivedPath != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, receivedPath)
	}
}

func TestHeartbeatNodeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-missing")
	err := client.Heartbeat(context.Background())
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound, got: %v", err)
	}
}

func TestHeartbeatOtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-err")
	err := client.Heartbeat(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNodeNotFound) {
		t.Error("should not be ErrNodeNotFound for 500")
	}
}

func TestStartHeartbeat(t *testing.T) {
	var mu sync.Mutex
	heartbeatCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		heartbeatCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-hb")
	// Override interval for faster testing.
	client.heartbeatInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = client.StartHeartbeat(ctx)

	// Wait enough time for at least 2 heartbeats.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	count := heartbeatCount
	mu.Unlock()

	if count < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", count)
	}
}

func TestStartHeartbeatCancellation(t *testing.T) {
	var mu sync.Mutex
	heartbeatCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		heartbeatCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-cancel")
	client.heartbeatInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errCh := client.StartHeartbeat(ctx)

	// Let one heartbeat fire.
	time.Sleep(75 * time.Millisecond)
	cancel()

	// Wait for goroutine to stop.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	countAfterCancel := heartbeatCount
	mu.Unlock()

	// Wait more to verify no new heartbeats fire.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	countLater := heartbeatCount
	mu.Unlock()

	if countLater != countAfterCancel {
		t.Errorf("heartbeats continued after cancel: %d -> %d", countAfterCancel, countLater)
	}

	// Channel should be closed eventually.
	select {
	case _, ok := <-errCh:
		if ok {
			// got a value, that's fine
		}
		// closed, that's fine too
	case <-time.After(500 * time.Millisecond):
		t.Error("error channel was not closed after context cancellation")
	}
}

func TestStartHeartbeatNodeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "node-gone")
	client.heartbeatInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := client.StartHeartbeat(ctx)

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("expected ErrNodeNotFound, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for ErrNodeNotFound")
	}
}

func TestUnregisterAll(t *testing.T) {
	var mu sync.Mutex
	var deletions []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		mu.Lock()
		deletions = append(deletions, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")

	node := &Node{ID: "node-1"}
	device := &Device{ID: "device-1"}
	sources := []*Source{{ID: "src-1"}, {ID: "src-2"}}
	flows := []*Flow{{ID: "flow-1"}}
	senders := []*Sender{{ID: "sender-1"}}
	receivers := []*Receiver{{ID: "receiver-1"}}

	err := client.UnregisterAll(context.Background(), node, device, sources, flows, senders, receivers)
	if err != nil {
		t.Fatalf("UnregisterAll returned error: %v", err)
	}

	// Reverse dependency order: senders+receivers, flows, sources, device, node
	base := "/x-nmos/registration/v1.3/resource"
	expected := []string{
		base + "/senders/sender-1",
		base + "/receivers/receiver-1",
		base + "/flows/flow-1",
		base + "/sources/src-2",
		base + "/sources/src-1",
		base + "/devices/device-1",
		base + "/nodes/node-1",
	}

	if len(deletions) != len(expected) {
		t.Fatalf("expected %d deletions, got %d: %v", len(expected), len(deletions), deletions)
	}
	for i, exp := range expected {
		if deletions[i] != exp {
			t.Errorf("deletion[%d]: expected %q, got %q", i, exp, deletions[i])
		}
	}
}

func TestUnregisterAllContinuesOnError(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First deletion fails.
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "delete failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewRegistrationClient(srv.URL, "test-node-id")
	err := client.UnregisterAll(context.Background(),
		&Node{ID: "n"}, &Device{ID: "d"},
		[]*Source{{ID: "s"}}, []*Flow{{ID: "f"}},
		[]*Sender{{ID: "snd"}}, []*Receiver{{ID: "rcv"}},
	)

	// Should return the first error.
	if err == nil {
		t.Fatal("expected error from failed deletion")
	}

	// But should have continued to delete all resources (best-effort).
	// snd + rcv + f + s + d + n = 6 total
	if callCount != 6 {
		t.Errorf("expected 6 DELETE calls (best-effort), got %d", callCount)
	}
}

