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

// Caller classes on switchframe_http_requests_total: which credential a
// request was let in, or turned away, on.
const (
	CallerToken     = "token"     // the session API token
	CallerOperator  = "operator"  // a registered operator's token
	CallerTicket    = "ticket"    // a one-time download ticket
	CallerInvalid   = "invalid"   // a bearer token that matches neither
	CallerNone      = "none"      // no bearer token
	CallerExempt    = "exempt"    // a path that takes no API credential
	CallerUnchecked = "unchecked" // no authentication ran, as in demo mode
)

// ctxKeyCaller is the context key under which MetricsMiddleware leaves a
// slot for the authentication further in to name the caller class.
type ctxKeyCaller struct{}

// setCaller records the caller class of r for MetricsMiddleware. Outside
// it, there is no slot and the call does nothing.
func setCaller(r *http.Request, class string) {
	if p, ok := r.Context().Value(ctxKeyCaller{}).(*string); ok {
		*p = class
	}
}

// MetricsMiddleware records Prometheus HTTP request metrics (counter and histogram)
// for each request. It uses r.Pattern (Go 1.22+ ServeMux feature) for route labels
// to avoid cardinality explosion from path parameters.
func MetricsMiddleware(next http.Handler) http.Handler {
	return metricsHandler(nil, next)
}

// RouteMetricsMiddleware is MetricsMiddleware for a chain that ends in
// routes. The route label is resolved against routes before the request
// is served, so a request rejected before it reaches the mux, by
// authentication for one, is still counted under its route rather than
// under the pattern of an outer mux.
func RouteMetricsMiddleware(routes *http.ServeMux) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return metricsHandler(routes, next)
	}
}

func metricsHandler(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		metrics.HTTPRequestsInFlight.Inc()
		defer metrics.HTTPRequestsInFlight.Dec()

		var pattern string
		if routes != nil {
			_, pattern = routes.Handler(r)
		}

		caller := CallerUnchecked
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyCaller{}, &caller))

		sr := &statusRecorder{ResponseWriter: w, status: 0}
		next.ServeHTTP(sr, r)

		status := sr.status
		if status == 0 {
			status = http.StatusOK
		}

		// Without routes, r.Pattern is what the mux that served the request
		// set: a pattern like "GET /api/sources" rather than the actual path,
		// which could have unbounded cardinality. With routes it is not read:
		// a request authentication turned away still carries the pattern of
		// an outer mux, which names no route.
		if pattern == "" && routes == nil {
			pattern = r.Pattern
		}
		if pattern == "" {
			pattern = "unknown"
		}

		metrics.HTTPRequestsTotal.WithLabelValues(
			r.Method,
			pattern,
			strconv.Itoa(status),
			caller,
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

// isLargeUploadPath reports whether the request path is one of the dedicated
// file-upload endpoints that legitimately accept bodies larger than
// maxJSONBodySize. These handlers each install their own http.MaxBytesReader
// (clip: 2GB, stinger/sequence: 256MB, stmap: 64MB, etc.), so they remain
// bounded; the global JSON cap is only skipped here to avoid clipping those
// larger limits.
//
// The exemption is keyed on the route path (NOT the Content-Type), so a client
// cannot bypass the cap on an arbitrary endpoint merely by sending a different
// Content-Type. Both the "/api/" and forward-compatible "/api/v1/" prefixes are
// matched, since this middleware runs before the version-prefix rewrite.
func isLargeUploadPath(path string) bool {
	// Normalize the versioned prefix so a single set of patterns covers both.
	if rest, ok := strings.CutPrefix(path, "/api/v1/"); ok {
		path = "/api/" + rest
	}
	switch path {
	case "/api/clips/upload", // multipart media clip (2GB)
		"/api/colorgrade/upload", // multipart .cube LUT
		"/api/wipes/custom":      // multipart grayscale PNG
		return true
	}
	// Path-variable routes: stmap upload, stinger raw upload, graphics
	// image/sequence uploads. Each handler self-bounds the body.
	if strings.HasPrefix(path, "/api/stmap/upload/") {
		return true
	}
	if strings.HasPrefix(path, "/api/stinger/") && strings.HasSuffix(path, "/upload") {
		return true
	}
	if strings.HasPrefix(path, "/api/graphics/") &&
		(strings.HasSuffix(path, "/image") || strings.HasSuffix(path, "/sequence")) {
		return true
	}
	return false
}

// MaxBytesMiddleware wraps request bodies with http.MaxBytesReader to reject
// oversized payloads before they are fully read. This prevents memory exhaustion
// from malicious or accidental large POST bodies. Excluded:
//   - GET/HEAD requests (no body)
//   - the dedicated file-upload endpoints (see isLargeUploadPath), which install
//     their own, larger per-handler limits
//
// The exemption is path-based rather than Content-Type-based: a previous version
// skipped the cap for application/octet-stream, application/zip, and multipart
// bodies, which let a client send an arbitrarily large body to ANY endpoint
// (e.g. POST /api/switch/cut) simply by setting Content-Type: application/octet-stream,
// defeating the cap and enabling a memory-exhaustion DoS via io.ReadAll.
func MaxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil {
			if !isLargeUploadPath(r.URL.Path) {
				r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
			}
		}
		next.ServeHTTP(w, r)
	})
}
