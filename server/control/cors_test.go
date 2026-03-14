package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware_PreflightReturns204(t *testing.T) {
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}
	if innerCalled {
		t.Error("inner handler should not be called for preflight OPTIONS request")
	}

	// Check all CORS headers are set.
	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_NormalRequestSetsCORSHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Errorf("expected body %q, got %q", `{"status":"ok"}`, rec.Body.String())
	}

	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_NoOriginStillWorks(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware(nil)(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	// No Origin header set.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// CORS headers should still be present even without an Origin header.
	assertCORSHeaders(t, rec, "*")
}

func TestCORSMiddleware_AllowedOrigins_MatchingOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"http://localhost:5173", "https://app.example.com"})(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	req.Header.Set("Origin", "http://localhost:5173")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "http://localhost:5173", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("expected Vary: Origin header when allowedOrigins is set")
	}
}

func TestCORSMiddleware_AllowedOrigins_NonMatchingOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"https://app.example.com"})(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/switch/state", nil)
	req.Header.Set("Origin", "http://evil.example.com")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin header, got %q", got)
	}
}

func TestCORSMiddleware_AllowedOrigins_PreflightMatch(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware([]string{"http://localhost:5173"})(inner)

	req := httptest.NewRequest(http.MethodOptions, "/api/switch/cut", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://localhost:5173" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "http://localhost:5173", got)
	}
}

// assertCORSHeaders checks that all expected CORS headers are set on the response.
func assertCORSHeaders(t *testing.T, rec *httptest.ResponseRecorder, expectedOrigin string) {
	t.Helper()

	expectations := map[string]string{
		"Access-Control-Allow-Origin":  expectedOrigin,
		"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization",
		"Access-Control-Max-Age":       "86400",
	}

	for header, expected := range expectations {
		got := rec.Header().Get(header)
		if got != expected {
			t.Errorf("header %s: expected %q, got %q", header, expected, got)
		}
	}
}
