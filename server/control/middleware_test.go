package control

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
