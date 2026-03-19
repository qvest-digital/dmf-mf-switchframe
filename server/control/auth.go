package control

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// authExemptPaths lists URL paths that do not require authentication.
// These include the certificate hash endpoint (needed for WebTransport
// bootstrapping), health checks, metrics scraping, and operator
// registration (authenticated via invite token instead of session API token).
var authExemptPaths = map[string]bool{
	"/api/cert-hash":              true,
	"/health":                     true,
	"/metrics":                    true,
	"/api/operator/register":      true,
	"/api/v1/operator/register":   true,
	"/api/operator/reconnect":     true,
	"/api/v1/operator/reconnect":  true,
	"/api/operator/heartbeat":     true,
	"/api/v1/operator/heartbeat":  true,
}

// OperatorTokenChecker checks if a token belongs to a registered operator.
// Used by AuthMiddleware to accept operator tokens alongside the session API token.
type OperatorTokenChecker func(token string) bool

// AuthMiddleware returns HTTP middleware that enforces Bearer token
// authentication on all requests except exempt paths. It accepts the
// session API token (timing-safe comparison) and optionally operator
// tokens via the checker function.
func AuthMiddleware(token string, operatorCheck ...OperatorTokenChecker) func(http.Handler) http.Handler {
	tokenBytes := []byte(token)
	var checkOperator OperatorTokenChecker
	if len(operatorCheck) > 0 {
		checkOperator = operatorCheck[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for exempt paths.
			if authExemptPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Extract Bearer token.
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("WWW-Authenticate", `Bearer realm="switchframe"`)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"missing or invalid authorization header"}`))
				return
			}

			provided := strings.TrimPrefix(authHeader, "Bearer ")

			// Check session API token first (timing-safe).
			if subtle.ConstantTimeCompare([]byte(provided), tokenBytes) == 1 {
				next.ServeHTTP(w, r)
				return
			}

			// Fall back to operator token check.
			if checkOperator != nil && checkOperator(provided) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer realm="switchframe"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
		})
	}
}

// NoopAuthMiddleware returns middleware that passes all requests through
// without authentication. Used in demo mode for ease of use.
func NoopAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}

// GenerateToken creates a cryptographically random 32-byte token
// encoded as a 64-character hexadecimal string.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
