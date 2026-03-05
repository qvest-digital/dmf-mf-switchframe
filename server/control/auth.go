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
// bootstrapping), health checks, and metrics scraping.
var authExemptPaths = map[string]bool{
	"/api/cert-hash": true,
	"/health":        true,
	"/metrics":       true,
}

// AuthMiddleware returns HTTP middleware that enforces Bearer token
// authentication on all requests except exempt paths. It uses
// crypto/subtle.ConstantTimeCompare for timing-safe token comparison
// to prevent timing side-channel attacks.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for exempt paths.
			if authExemptPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Extract and validate Bearer token.
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("WWW-Authenticate", `Bearer realm="switchframe"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"missing or invalid authorization header"}`))
				return
			}

			provided := []byte(strings.TrimPrefix(authHeader, "Bearer "))
			if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("WWW-Authenticate", `Bearer realm="switchframe"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"invalid token"}`))
				return
			}

			next.ServeHTTP(w, r)
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
