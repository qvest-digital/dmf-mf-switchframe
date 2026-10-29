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
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/metrics"
)

// ctxKeyLogger is the unexported context key for the request-scoped logger.
type ctxKeyLogger struct{}

// LoggerMiddleware returns middleware that adds structured logging each request.
// It generates or preserves request ID, creates child logger with request
// attributes, and logs completed request with status, latency, and bytes written.
//
// A successful read is logged at DEBUG: the UI polls, so at INFO the reads
// bury everything else. Mutations and every failed request stay at INFO. A
// failure that repeats, one client retrying a refused token for instance,
// is logged once per errorLogWindow with the count left out since.
func LoggerMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return loggerMiddleware(base, newErrorLogLimiter(errorLogWindow, time.Now))
}

func loggerMiddleware(base *slog.Logger, limiter *errorLogLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Use incoming X-Request-ID or generate new one.
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = newRequestID()
			}

			// Create child logger request attributes.
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

			// Wrap ResponseWriter capture status and bytes.
			sr := &statusRecorder{ResponseWriter: w, status: 0}
			next.ServeHTTP(sr, r)

			// Default to 200 if WriteHeader never called.
			status := sr.status
			if status == 0 {
				status = http.StatusOK
			}

			latency := time.Since(start)
			attrs := []any{
				"status", status,
				"latency", latency,
				"bytes", sr.written,
			}

			level := slog.LevelInfo
			switch {
			case isNoisyPath(r.URL.Path), isRead(r.Method) && status < http.StatusBadRequest:
				level = slog.LevelDebug
			case status >= http.StatusBadRequest:
				log, suppressed := limiter.allow(r.Method + " " + r.URL.Path + " " + strconv.Itoa(status))
				if !log {
					return
				}
				// Behind an ingress every request comes from the ingress, so
				// only these name the client that keeps failing.
				attrs = append(attrs,
					"user_agent", r.UserAgent(),
					"forwarded_for", r.Header.Get("X-Forwarded-For"),
				)
				if suppressed > 0 {
					attrs = append(attrs, "suppressed", suppressed)
				}
			}
			child.Log(r.Context(), level, "http request", attrs...)
		})
	}
}

// isRead reports whether a request method only reads.
func isRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// errorLogWindow is how long identical failed requests share one log line.
const errorLogWindow = 10 * time.Second

// errorLogLimiterMaxKeys bounds the keys held; paths carry ids, so a client
// walking ids would otherwise grow the map without end.
const errorLogLimiterMaxKeys = 1024

// errorLogLimiter lets one log line per key through each window and counts
// the rest, so the next line can say how many were left out.
type errorLogLimiter struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	keys   map[string]*errorLogKey
}

type errorLogKey struct {
	logged     time.Time
	suppressed int
}

func newErrorLogLimiter(window time.Duration, now func() time.Time) *errorLogLimiter {
	return &errorLogLimiter{window: window, now: now, keys: make(map[string]*errorLogKey)}
}

// allow reports whether a request under key is logged, and how many under
// the same key were not since the last one that was.
func (l *errorLogLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	k, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= errorLogLimiterMaxKeys {
			clear(l.keys)
		}
		l.keys[key] = &errorLogKey{logged: now}
		return true, 0
	}
	if now.Sub(k.logged) < l.window {
		k.suppressed++
		return false, 0
	}
	suppressed := k.suppressed
	k.logged, k.suppressed = now, 0
	return true, suppressed
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
