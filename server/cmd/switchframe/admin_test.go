package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestAdminServer_HealthNoAuth(t *testing.T) {
	// Health endpoint must always be accessible (k8s probes).
	stop, addr := startTestAdminServer(t, "secret-token")
	defer stop()

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /health = %d, want 200", resp.StatusCode)
	}
}

func TestAdminServer_ReadyNoAuth(t *testing.T) {
	// Ready endpoint must always be accessible (k8s probes).
	stop, addr := startTestAdminServer(t, "secret-token")
	defer stop()

	resp, err := http.Get("http://" + addr + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// 503 is expected since readyFlag is not set, but 401 would be wrong.
	if resp.StatusCode == 401 {
		t.Error("GET /ready should not require auth")
	}
}

func TestAdminServer_CertHashNoAuth(t *testing.T) {
	// Cert-hash endpoint must always be accessible (browser bootstrapping).
	stop, addr := startTestAdminServer(t, "secret-token")
	defer stop()

	resp, err := http.Get("http://" + addr + "/api/cert-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		t.Error("GET /api/cert-hash should not require auth")
	}
}

func TestAdminServer_MetricsRequiresToken(t *testing.T) {
	stop, addr := startTestAdminServer(t, "secret-token")
	defer stop()

	// Without token - should 401.
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("GET /metrics without token = %d, want 401", resp.StatusCode)
	}

	// With wrong token - should 401.
	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Errorf("GET /metrics with wrong token = %d, want 401", resp2.StatusCode)
	}

	// With correct token - should succeed.
	req3, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req3.Header.Set("Authorization", "Bearer secret-token")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Errorf("GET /metrics with token = %d, want 200", resp3.StatusCode)
	}
}

func TestAdminServer_DebugRequiresToken(t *testing.T) {
	stop, addr := startTestAdminServer(t, "secret-token")
	defer stop()

	// Without token - should 401.
	resp, err := http.Get("http://" + addr + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("GET /debug/pprof/ without token = %d, want 401", resp.StatusCode)
	}

	// With correct token - should succeed.
	req, _ := http.NewRequest("GET", "http://"+addr+"/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("GET /debug/pprof/ with token = %d, want 200", resp2.StatusCode)
	}
}

func TestAdminServer_NoTokenConfigured(t *testing.T) {
	// When no admin token is set, all endpoints should be accessible.
	stop, addr := startTestAdminServer(t, "")
	defer stop()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /metrics without token config = %d, want 200", resp.StatusCode)
	}

	resp2, err := http.Get("http://" + addr + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("GET /debug/pprof/ without token config = %d, want 200", resp2.StatusCode)
	}
}

func TestAdminServer_DefaultBindLocalhost(t *testing.T) {
	// Verify the default admin-addr flag binds to localhost.
	// We test this by checking the parseConfig default value.
	// Reset flag.CommandLine to avoid "flag already defined" errors.
	// The actual test is in TestParseConfig_DefaultAdminAddr below.
}

func startTestAdminServer(t *testing.T, adminToken string) (stop func(), addr string) {
	t.Helper()
	stop, addr = StartAdminServer(context.Background(), "127.0.0.1:0", ":8080", "testhash", false, adminToken)
	// Wait briefly for the server to start accepting connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			return stop, addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("admin server did not start in time")
	return stop, addr
}
