package control

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware_RejectsUnauthenticated(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware("secret-token-123")(inner)

	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, `Bearer realm="switchframe"`, rec.Header().Get("WWW-Authenticate"))
}

func TestAuthMiddleware_AcceptsValidBearer(t *testing.T) {
	token := "my-valid-token-abc"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler := AuthMiddleware(token)(inner)

	req := httptest.NewRequest("GET", "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

func TestAuthMiddleware_RejectsInvalidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware("correct-token")(inner)

	req := httptest.NewRequest("GET", "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, `Bearer realm="switchframe"`, rec.Header().Get("WWW-Authenticate"))
}

func TestAuthMiddleware_RejectsMalformedHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware("correct-token")(inner)

	// No "Bearer " prefix
	req := httptest.NewRequest("GET", "/api/sources", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, `Bearer realm="switchframe"`, rec.Header().Get("WWW-Authenticate"))
}

func TestAuthMiddleware_ExemptPaths(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("passed"))
	})
	handler := AuthMiddleware("secret")(inner)

	exemptPaths := []string{"/api/cert-hash", "/health", "/metrics"}
	for _, path := range exemptPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			// No Authorization header
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "path %s should be exempt from auth", path)
			require.Equal(t, "passed", rec.Body.String())
		})
	}
}

func TestAuthMiddleware_ExemptPathsWithQueryParams(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware("secret")(inner)

	req := httptest.NewRequest("GET", "/api/cert-hash?foo=bar", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestGenerateToken_Returns64CharHex(t *testing.T) {
	token, err := GenerateToken()
	require.NoError(t, err)
	require.Len(t, token, 64, "token should be 64 hex characters (32 bytes)")

	// Verify it's valid hex
	_, err = hex.DecodeString(token)
	require.NoError(t, err, "token should be valid hex")
}

func TestGenerateToken_Unique(t *testing.T) {
	token1, err := GenerateToken()
	require.NoError(t, err)
	token2, err := GenerateToken()
	require.NoError(t, err)

	require.NotEqual(t, token1, token2, "two generated tokens should not be equal")
}
