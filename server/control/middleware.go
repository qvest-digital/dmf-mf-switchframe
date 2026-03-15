package control

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zsiec/switchframe/server/metrics"
)

// ctxKeyLogger is the unexported context key for the request-scoped logger.
type ctxKeyLogger struct{}

// LoggerMiddleware returns middleware that adds structured logging to each request.
// It generates or preserves a request ID, creates a child logger with request
// attributes, and logs the completed request with status, latency, and bytes written.
func LoggerMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Use incoming X-Request-ID or generate a new one.
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = newRequestID()
			}

			// Create child logger with request attributes.
			child := base.With(
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
			)

			// Store child logger in context.
			ctx := context.WithValue(r.Context(), ctxKeyLogger{}, child)
			r = r.WithContext(ctx)

			// Set response header.
			w.Header().Set("X-Request-ID", reqID)

			// Wrap ResponseWriter to capture status and bytes.
			sr := &statusRecorder{ResponseWriter: w, status: 0}

			next.ServeHTTP(sr, r)

			// Default to 200 if WriteHeader was never called.
			status := sr.status
			if status == 0 {
				status = http.StatusOK
			}

			latency := time.Since(start)

			// Log at DEBUG for noisy paths, INFO for everything else.
			level := slog.LevelInfo
			if isNoisyPath(r.URL.Path) {
				level = slog.LevelDebug
			}

			child.Log(r.Context(), level, "http request",
				"status", status,
				"latency", latency,
				"bytes", sr.written,
			)
		})
	}
}

// LogFromCtx retrieves the request-scoped logger from the context.
// If no logger is found (e.g., called outside middleware), it returns slog.Default().
func LogFromCtx(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// MetricsMiddleware records Prometheus HTTP request metrics (counter and histogram)
// for each request. It uses r.Pattern (Go 1.22+ ServeMux feature) for route labels
// to avoid cardinality explosion from path parameters.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		sr := &statusRecorder{ResponseWriter: w, status: 0}
		next.ServeHTTP(sr, r)

		status := sr.status
		if status == 0 {
			status = http.StatusOK
		}

		// Use r.Pattern for the route label. This is set by Go 1.22+ ServeMux
		// after routing, so it contains the pattern like "GET /api/sources"
		// rather than the actual path (which could have unbounded cardinality).
		pattern := r.Pattern
		if pattern == "" {
			pattern = "unknown"
		}

		metrics.HTTPRequestsTotal.WithLabelValues(
			r.Method,
			pattern,
			strconv.Itoa(status),
		).Inc()

		metrics.HTTPRequestDuration.WithLabelValues(
			r.Method,
			pattern,
		).Observe(time.Since(start).Seconds())
	})
}

// statusRecorder wraps http.ResponseWriter to capture the response status code
// and total bytes written.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

// WriteHeader captures the status code before delegating to the underlying writer.
func (sr *statusRecorder) WriteHeader(code int) {
	if sr.status == 0 {
		sr.status = code
	}
	sr.ResponseWriter.WriteHeader(code)
}

// Write captures the number of bytes written and delegates to the underlying writer.
func (sr *statusRecorder) Write(b []byte) (int, error) {
	n, err := sr.ResponseWriter.Write(b)
	sr.written += int64(n)
	return n, err
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
// This is required for streaming responses (e.g., SSE, chunked transfer) to work
// correctly when the ResponseWriter is wrapped.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the underlying ResponseWriter if it implements http.Hijacker.
// This is required for WebSocket upgrades and other connection hijacking to work
// correctly when the ResponseWriter is wrapped.
func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := sr.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// Unwrap returns the underlying ResponseWriter, allowing http.ResponseController
// and other standard library mechanisms to discover optional interfaces.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// newRequestID generates a random 8-byte hex string for request tracing.
func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: should never happen in practice.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// isNoisyPath returns true for paths that are polled frequently and should
// be logged at DEBUG level to avoid log spam.
func isNoisyPath(path string) bool {
	switch path {
	case "/api/switch/state", "/metrics":
		return true
	}
	return false
}

// maxJSONBodySize is the default maximum request body size for JSON POST endpoints.
// Sized to accommodate the graphics frame upload endpoint which sends base64-encoded
// RGBA overlays (up to ~11 MB for 1080p). Clip and stinger uploads use multipart/zip
// content types and are excluded from this middleware.
const maxJSONBodySize = 16 << 20 // 16 MB

// MaxBytesMiddleware wraps request bodies with http.MaxBytesReader to reject
// oversized payloads before they are fully read. This prevents memory exhaustion
// from malicious or accidental large POST bodies. Excluded:
//   - GET/HEAD requests (no body)
//   - multipart/form-data uploads (stinger zip, clip upload have their own limits)
//   - application/octet-stream uploads (stinger raw upload)
func MaxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil {
			ct := r.Header.Get("Content-Type")
			// Skip file uploads — they have per-handler size limits.
			if !strings.HasPrefix(ct, "multipart/") && ct != "application/octet-stream" && ct != "application/zip" {
				r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
			}
		}
		next.ServeHTTP(w, r)
	})
}
