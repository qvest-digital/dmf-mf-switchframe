package control

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zsiec/switchframe/server/metrics"
)

func TestRequestIDGenerated(t *testing.T) {
	handler := LoggerMiddleware(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	rid := rec.Header().Get("X-Request-ID")
	if rid == "" {
		t.Fatal("X-Request-ID response header not set")
	}
	// 8 random bytes = 16 hex chars
	if len(rid) != 16 {
		t.Errorf("X-Request-ID length = %d, want 16 hex chars; got %q", len(rid), rid)
	}
}

func TestRequestIDPreserved(t *testing.T) {
	handler := LoggerMiddleware(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	req.Header.Set("X-Request-ID", "incoming-id-1234")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	rid := rec.Header().Get("X-Request-ID")
	if rid != "incoming-id-1234" {
		t.Errorf("X-Request-ID = %q, want %q", rid, "incoming-id-1234")
	}
}

func TestStatusRecorderCapturesStatus(t *testing.T) {
	var capturedStatus int
	handler := LoggerMiddleware(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))

	// We need a way to observe the captured status. We'll verify via the log
	// output and the response code on the recorder.
	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	capturedStatus = rec.Code
	if capturedStatus != http.StatusNotFound {
		t.Errorf("status = %d, want %d", capturedStatus, http.StatusNotFound)
	}
}

func TestStatusRecorderCapturesBytes(t *testing.T) {
	// Use a buffer to capture log output and verify bytes written.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello world")) // 11 bytes
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "bytes=11") {
		t.Errorf("log output does not contain bytes=11: %s", logOutput)
	}
}

func TestStatusRecorderDefaultStatus(t *testing.T) {
	// If WriteHeader is never called, status should be 200 (implicit).
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "status=200") {
		t.Errorf("log output does not contain status=200: %s", logOutput)
	}
}

func TestLogFromCtxReturnsLoggerWhenSet(t *testing.T) {
	var capturedLogger *slog.Logger
	handler := LoggerMiddleware(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLogger = LogFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedLogger == nil {
		t.Fatal("LogFromCtx returned nil")
	}
	// Should not be the default logger — it should be a child with request attrs.
	if capturedLogger == slog.Default() {
		t.Error("LogFromCtx returned slog.Default(), want child logger with request attrs")
	}
}

func TestLogFromCtxFallsBackToDefault(t *testing.T) {
	logger := LogFromCtx(context.Background())
	if logger != slog.Default() {
		t.Error("LogFromCtx without middleware should return slog.Default()")
	}
}

func TestNoisyPathLoggedAtDebug(t *testing.T) {
	// At INFO level, noisy paths should NOT appear in output.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if buf.Len() != 0 {
		t.Errorf("noisy path /api/switch/state should be logged at DEBUG, not visible at INFO level; got: %s", buf.String())
	}
}

func TestNoisyPathVisibleAtDebugLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if buf.Len() == 0 {
		t.Error("noisy path /api/switch/state should be visible at DEBUG level")
	}
}

func TestNonNoisyPathLoggedAtInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if buf.Len() == 0 {
		t.Error("/api/switch/cut should be logged at INFO level")
	}
}

func TestMetricsMiddlewareIncrementsCounter(t *testing.T) {
	// Use the production MetricsMiddleware which uses the package-level metrics.
	// We verify by checking the production registry after the call.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := MetricsMiddleware(mux)

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify production-level metrics were incremented.
	// Gather from the production registry.
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == "switchframe_http_requests_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("switchframe_http_requests_total metric not found in registry after request")
	}
}

func TestMetricsMiddlewareRecordsDuration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := MetricsMiddleware(mux)

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == "switchframe_http_request_duration_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Error("switchframe_http_request_duration_seconds metric not found in registry after request")
	}
}

func TestLoggerMiddlewareLogFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	for _, field := range []string{"request_id=", "method=POST", "path=/api/switch/cut", "status=201", "latency=", "bytes=7"} {
		if !strings.Contains(logOutput, field) {
			t.Errorf("log output missing field %q: %s", field, logOutput)
		}
	}
}
