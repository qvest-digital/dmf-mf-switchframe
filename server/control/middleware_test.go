package control

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
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
	require.NotEmpty(t, rid, "X-Request-ID response header not set")
	// 8 random bytes = 16 hex chars
	require.Len(t, rid, 16, "X-Request-ID should be 16 hex chars; got %q", rid)
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
	require.Equal(t, "incoming-id-1234", rid)
}

func TestStatusRecorderCapturesStatus(t *testing.T) {
	var capturedStatus int
	handler := LoggerMiddleware(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))

	// We need a way to observe the captured status. We'll verify via the log
	// output and the response code on the recorder.
	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	capturedStatus = rec.Code
	require.Equal(t, http.StatusNotFound, capturedStatus)
}

func TestStatusRecorderCapturesBytes(t *testing.T) {
	// Use a buffer to capture log output and verify bytes written.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world")) // 11 bytes
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	require.Contains(t, logOutput, "bytes=11")
}

func TestStatusRecorderDefaultStatus(t *testing.T) {
	// If WriteHeader is never called, status should be 200 (implicit).
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	require.Contains(t, logOutput, "status=200")
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

	require.NotNil(t, capturedLogger, "LogFromCtx returned nil")
	// Should not be the default logger — it should be a child with request attrs.
	require.NotEqual(t, slog.Default(), capturedLogger,
		"LogFromCtx returned slog.Default(), want child logger with request attrs")
}

func TestLogFromCtxFallsBackToDefault(t *testing.T) {
	logger := LogFromCtx(context.Background())
	require.Equal(t, slog.Default(), logger,
		"LogFromCtx without middleware should return slog.Default()")
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

	require.Empty(t, buf.String(),
		"noisy path /api/switch/state should be logged at DEBUG, not visible at INFO level")
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

	require.NotEmpty(t, buf.String(), "noisy path /api/switch/state should be visible at DEBUG level")
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

	require.NotEmpty(t, buf.String(), "/api/switch/cut should be logged at INFO level")
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
	families, err := metrics.GetRegistry().Gather()
	require.NoError(t, err)
	found := false
	for _, f := range families {
		if f.GetName() == "switchframe_http_requests_total" {
			found = true
			break
		}
	}
	require.True(t, found, "switchframe_http_requests_total metric not found in registry after request")
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

	families, err := metrics.GetRegistry().Gather()
	require.NoError(t, err)
	found := false
	for _, f := range families {
		if f.GetName() == "switchframe_http_request_duration_seconds" {
			found = true
			break
		}
	}
	require.True(t, found, "switchframe_http_request_duration_seconds metric not found in registry after request")
}

func TestLoggerMiddlewareLogFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	for _, field := range []string{"request_id=", "method=POST", "path=/api/switch/cut", "status=201", "latency=", "bytes=7"} {
		require.Contains(t, logOutput, field, "log output missing field %q", field)
	}
}

func TestMaxBytesMiddleware_RejectsOversizedBody(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the body -- should fail for oversized payloads.
		buf := make([]byte, maxJSONBodySize+1)
		_, err := r.Body.Read(buf)
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Create a body that exceeds the limit.
	bigBody := make([]byte, maxJSONBodySize+1)
	req := httptest.NewRequest("POST", "/api/switch/cut", bytes.NewReader(bigBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
		"oversized POST body should be rejected")
}

func TestMaxBytesMiddleware_AllowsNormalBody(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf[:n])
	}))

	smallBody := []byte(`{"source":"camera1"}`)
	req := httptest.NewRequest("POST", "/api/switch/cut", bytes.NewReader(smallBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"normal-sized POST body should be allowed")
}

func TestMaxBytesMiddleware_SkipsGETRequests(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMaxBytesMiddleware_SkipsMultipartUploads(t *testing.T) {
	// Multipart uploads (stinger, clip) have their own size limits.
	// The middleware should not wrap their body.
	bigBody := make([]byte, maxJSONBodySize+1)
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, len(bigBody))
		n, _ := r.Body.Read(buf)
		if n > 0 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	}))

	req := httptest.NewRequest("POST", "/api/clips/upload", bytes.NewReader(bigBody))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"multipart uploads should bypass body size limit")
}

// TestMaxBytesMiddleware_OctetStreamCannotBypassCapOnCommandPath is the
// regression test for the Content-Type bypass: previously any request whose
// Content-Type was application/octet-stream (or zip/multipart) skipped the cap
// on EVERY path, so a giant body to /api/switch/cut would be read whole into
// memory. The cap is now keyed on the route path, so non-upload endpoints stay
// bounded regardless of Content-Type.
func TestMaxBytesMiddleware_OctetStreamCannotBypassCapOnCommandPath(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler reads the whole body, mirroring timedHandler/io.ReadAll.
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	for _, ct := range []string{"application/octet-stream", "application/zip", "multipart/form-data; boundary=x"} {
		bigBody := make([]byte, maxJSONBodySize+1)
		req := httptest.NewRequest("POST", "/api/switch/cut", bytes.NewReader(bigBody))
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
			"oversized body to /api/switch/cut with Content-Type %q must still be capped", ct)
	}
}

// TestMaxBytesMiddleware_SnapshotPathCapped ensures the snapshot-apply endpoint
// is also bounded (it formerly relied on the bypassable middleware and did an
// unbounded io.ReadAll).
func TestMaxBytesMiddleware_SnapshotPathCapped(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	bigBody := make([]byte, maxJSONBodySize+1)
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", bytes.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code,
		"oversized snapshot body must be capped regardless of Content-Type")
}

// TestMaxBytesMiddleware_LargeUploadPathsExempt confirms the dedicated upload
// endpoints (which install their own larger per-handler limits) remain exempt
// from the global JSON cap, including via the /api/v1/ version prefix.
func TestMaxBytesMiddleware_LargeUploadPathsExempt(t *testing.T) {
	handler := MaxBytesMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Body must NOT be wrapped: a >cap read should succeed here.
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	paths := []string{
		"/api/clips/upload",
		"/api/colorgrade/upload",
		"/api/wipes/custom",
		"/api/stmap/upload/mymap",
		"/api/stinger/intro/upload",
		"/api/graphics/2/image",
		"/api/graphics/2/sequence",
		"/api/v1/clips/upload", // version-prefixed
		"/api/v1/stinger/intro/upload",
	}
	for _, p := range paths {
		bigBody := make([]byte, maxJSONBodySize+1)
		req := httptest.NewRequest("POST", p, bytes.NewReader(bigBody))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code,
			"large-upload path %q must be exempt from the global JSON cap", p)
	}
}

// requestsCount reads switchframe_http_requests_total for one label set.
func requestsCount(method, pattern, status, caller string) float64 {
	return testutil.ToFloat64(metrics.HTTPRequestsTotal.WithLabelValues(method, pattern, status, caller))
}

// metricsTestServer wires the API chain as the app does: an outer mux hands
// /api/ to metrics -> auth -> the API mux.
func metricsTestServer() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/metricstest/clips/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	api.HandleFunc("GET /api/peer/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	operatorCheck := OperatorTokenChecker(func(token string) bool { return token == "op-token" })
	h := AuthMiddleware("api-token", operatorCheck)(api)
	h = RouteMetricsMiddleware(api)(h)
	top := http.NewServeMux()
	top.Handle("/api/", h)
	return top
}

// A request turned away by authentication never reaches the API mux. It
// must still be counted under its own route, not under the outer mux's
// "/api/", or a client looping on a rejected call cannot be told from all
// other traffic.
func TestRouteMetricsMiddleware_RejectedRequestKeepsRoute(t *testing.T) {
	h := metricsTestServer()
	const route = "GET /api/metricstest/clips/{id}"
	before := requestsCount("GET", route, "401", CallerNone)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/metricstest/clips/abc", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	require.Equal(t, before+1, requestsCount("GET", route, "401", CallerNone))
}

// The caller label names the credential a request came in on, so an
// operator UI, a script holding the API token, a stale token and an
// anonymous client are told apart.
func TestRouteMetricsMiddleware_CallerClass(t *testing.T) {
	h := metricsTestServer()
	const route = "GET /api/metricstest/clips/{id}"
	tests := []struct {
		name   string
		path   string
		auth   string
		route  string
		status int
		caller string
	}{
		{"api token", "/api/metricstest/clips/1", "Bearer api-token", route, http.StatusOK, CallerToken},
		{"operator token", "/api/metricstest/clips/2", "Bearer op-token", route, http.StatusOK, CallerOperator},
		{"unknown token", "/api/metricstest/clips/3", "Bearer stale", route, http.StatusUnauthorized, CallerInvalid},
		{"no token", "/api/metricstest/clips/4", "", route, http.StatusUnauthorized, CallerNone},
		{"exempt path", "/api/peer/health", "", "GET /api/peer/health", http.StatusOK, CallerExempt},
		{"no route", "/api/metricstest/nothing", "Bearer api-token", "unknown", http.StatusNotFound, CallerToken},
		{"no route, no token", "/api/metricstest/nothing", "", "unknown", http.StatusUnauthorized, CallerNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := strconv.Itoa(tt.status)
			before := requestsCount("GET", tt.route, status, tt.caller)
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, tt.status, rec.Code)
			require.Equal(t, before+1, requestsCount("GET", tt.route, status, tt.caller))
		})
	}
}

// Without authentication in the chain, as in demo mode, the caller is
// unchecked rather than claimed as any credential.
func TestMetricsMiddleware_CallerUnchecked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metricstest/demo", func(w http.ResponseWriter, r *http.Request) {})
	h := RouteMetricsMiddleware(mux)(mux)
	before := requestsCount("GET", "GET /api/metricstest/demo", "200", CallerUnchecked)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/metricstest/demo", nil))
	require.Equal(t, before+1, requestsCount("GET", "GET /api/metricstest/demo", "200", CallerUnchecked))
}

func TestMetricsMiddleware_InFlight(t *testing.T) {
	var during float64
	h := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		during = testutil.ToFloat64(metrics.HTTPRequestsInFlight)
	}))
	before := testutil.ToFloat64(metrics.HTTPRequestsInFlight)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/x", nil))
	require.Equal(t, before+1, during, "a request is in flight while served")
	require.Equal(t, before, testutil.ToFloat64(metrics.HTTPRequestsInFlight), "and no longer once answered")
}
