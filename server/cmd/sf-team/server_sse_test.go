package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSE_StreamsEvents(t *testing.T) {
	d, _ := newTestDB(t)
	srv := newServer(d, serverConfig{})
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(100 * time.Millisecond)
		ctx := context.Background()
		_ = d.register(ctx, "agent-x", "X", "/tmp/x", 1)
		_ = d.claim(ctx, "BUG-SSE", "agent-x", "", nil, false)
	}()

	client := ts.Client()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/events", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			continue
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "claim") {
			return
		}
	}
	t.Fatal("did not observe claim event within 3s")
}
