package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/switcher"
)

// setupOperatorTestAPI creates a test API with operator store and session manager
// in demo mode. Demo mode bypasses the operator-register auth gate so these
// tests can exercise handler internals (name validation, role validation,
// session wiring) via unauthenticated POSTs. The auth gate itself is covered
// by setupOperatorTestAPIBare + setupOperatorTestAPIWithSessionToken.
// The switcher is minimal (no sources) but non-nil to avoid panics in NewAPI.
func setupOperatorTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)

	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)

	api := NewAPI(sw,
		WithOperatorStore(store),
		WithSessionManager(sm),
		WithDemoMode(true),
	)

	mux := http.NewServeMux()
	api.RegisterOnMux(mux)

	return api, mux
}

// registerOperatorHelper is a test helper that registers an operator and returns the response.
func registerOperatorHelper(t *testing.T, mux *http.ServeMux, name string, role string) map[string]any {
	t.Helper()
	body := map[string]string{"name": name, "role": role}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "register %s: %s", name, rec.Body.String())
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return resp
}

// bearerRequest creates an HTTP request with a bearer token in the Authorization header.
func bearerRequest(method, path string, body any, token string) *http.Request {
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// --- Register tests ---

func TestOperatorRegister_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	resp := registerOperatorHelper(t, mux, "Alice", "director")

	// Verify required fields are present.
	require.Contains(t, resp, "id")
	require.Equal(t, "Alice", resp["name"])
	require.Equal(t, "director", resp["role"])
	require.Contains(t, resp, "token")
}

func TestOperatorRegister_EmptyName(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	body := map[string]string{"name": "", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

func TestOperatorRegister_InvalidRole(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	body := map[string]string{"name": "Bob", "role": "superadmin"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// --- Reconnect tests ---

func TestOperatorReconnect_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register first to get a token.
	regResp := registerOperatorHelper(t, mux, "Alice", "director")
	token := regResp["token"].(string)

	// Reconnect with the token.
	req := bearerRequest("POST", "/api/operator/reconnect", nil, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "Alice", resp["name"])
	require.Equal(t, "director", resp["role"])
}

func TestOperatorReconnect_InvalidToken(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	req := bearerRequest("POST", "/api/operator/reconnect", nil, "bogus-token-12345")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// --- Heartbeat tests ---

func TestOperatorHeartbeat_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	regResp := registerOperatorHelper(t, mux, "Alice", "director")
	token := regResp["token"].(string)

	req := bearerRequest("POST", "/api/operator/heartbeat", nil, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.True(t, resp["ok"], "expected ok=true in response")
}

func TestOperatorHeartbeat_InvalidToken(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	req := bearerRequest("POST", "/api/operator/heartbeat", nil, "bogus-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// --- List tests ---

func TestOperatorList(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register two operators.
	registerOperatorHelper(t, mux, "Alice", "director")
	registerOperatorHelper(t, mux, "Bob", "audio")

	req := httptest.NewRequest("GET", "/api/operator/list", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var result []operator.Info
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Both should be connected since register auto-connects.
	for _, op := range result {
		require.True(t, op.Connected, "operator %q: expected connected=true", op.Name)
	}
}

// --- Lock tests ---

func TestOperatorLock_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	regResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	token := regResp["token"].(string)

	req := bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.True(t, resp["ok"], "expected ok=true in response")
}

func TestOperatorLock_Conflict(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register two operators.
	audioResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	audioToken := audioResp["token"].(string)

	directorResp := registerOperatorHelper(t, mux, "Director", "director")
	directorToken := directorResp["token"].(string)

	// AudioOp locks "audio" subsystem.
	req := bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, audioToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "first lock: body: %s", rec.Body.String())

	// Director tries to lock the same subsystem — should get 409 conflict.
	req = bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, directorToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// --- Unlock tests ---

func TestOperatorUnlock_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	regResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	token := regResp["token"].(string)

	// Lock first.
	req := bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "lock: body: %s", rec.Body.String())

	// Unlock.
	req = bearerRequest("POST", "/api/operator/unlock",
		map[string]string{"subsystem": "audio"}, token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.True(t, resp["ok"], "expected ok=true in response")
}

// --- Force-unlock tests ---

func TestOperatorForceUnlock_DirectorOnly(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register director and audio operator.
	dirResp := registerOperatorHelper(t, mux, "Director", "director")
	dirToken := dirResp["token"].(string)

	audioResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	audioToken := audioResp["token"].(string)

	// AudioOp locks the audio subsystem.
	req := bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, audioToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "lock: body: %s", rec.Body.String())

	// Non-director (audio operator) tries to force-unlock — should fail 403.
	req = bearerRequest("POST", "/api/operator/force-unlock",
		map[string]string{"subsystem": "audio"}, audioToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "non-director force-unlock: body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var errResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	require.Contains(t, errResp, "error")

	// Director force-unlocks — should succeed.
	req = bearerRequest("POST", "/api/operator/force-unlock",
		map[string]string{"subsystem": "audio"}, dirToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "director force-unlock: body: %s", rec.Body.String())
}

// --- Delete tests ---

func TestOperatorDelete_SelfAllowed(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	regResp := registerOperatorHelper(t, mux, "Alice", "audio")
	token := regResp["token"].(string)
	id := regResp["id"].(string)

	// Alice deletes herself.
	req := bearerRequest("DELETE", "/api/operator/"+id, nil, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.True(t, resp["ok"], "expected ok=true in response")
}

func TestOperatorDelete_DirectorAllowed(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	dirResp := registerOperatorHelper(t, mux, "Director", "director")
	dirToken := dirResp["token"].(string)

	audioResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	audioID := audioResp["id"].(string)

	// Director deletes AudioOp.
	req := bearerRequest("DELETE", "/api/operator/"+audioID, nil, dirToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestOperatorDelete_Forbidden(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	audioResp := registerOperatorHelper(t, mux, "AudioOp", "audio")
	audioToken := audioResp["token"].(string)

	dirResp := registerOperatorHelper(t, mux, "Director", "director")
	dirID := dirResp["id"].(string)

	// Audio operator tries to delete Director — should be forbidden.
	req := bearerRequest("DELETE", "/api/operator/"+dirID, nil, audioToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// inviteTokenSessionAPIToken is the session API token wired into
// setupOperatorTestAPIWithInviteTokens, so tests can exercise the
// session-owner bypass that production deploys (app.go wires the session token
// unconditionally).
const inviteTokenSessionAPIToken = "session-token-invite-helper-0123456789"

// setupOperatorTestAPIWithInviteTokens creates a test API with invite tokens configured.
// The session API token is also wired to match production (app.go always passes
// WithSessionAPIToken alongside WithInviteTokens) so the session-owner bypass
// can be exercised.
func setupOperatorTestAPIWithInviteTokens(t *testing.T, tokens map[string]string) (*API, *http.ServeMux) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)

	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)

	api := NewAPI(sw,
		WithOperatorStore(store),
		WithSessionManager(sm),
		WithInviteTokens(tokens),
		WithSessionAPIToken(inviteTokenSessionAPIToken),
	)

	mux := http.NewServeMux()
	api.RegisterOnMux(mux)

	return api, mux
}

// setupOperatorTestAPIBare creates a test API without invite tokens, without a
// session API token, and without demo mode — the production-like configuration
// where unauthenticated register must be rejected.
func setupOperatorTestAPIBare(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)

	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)

	api := NewAPI(sw, WithOperatorStore(store), WithSessionManager(sm))

	mux := http.NewServeMux()
	api.RegisterOnMux(mux)

	return api, mux
}

// setupOperatorTestAPIWithSessionToken creates a test API with a session API
// token but no invite tokens and not in demo mode — the production "owner can
// bootstrap themselves" configuration.
func setupOperatorTestAPIWithSessionToken(t *testing.T, sessionToken string) (*API, *http.ServeMux) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)

	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)

	api := NewAPI(sw,
		WithOperatorStore(store),
		WithSessionManager(sm),
		WithSessionAPIToken(sessionToken),
	)

	mux := http.NewServeMux()
	api.RegisterOnMux(mux)

	return api, mux
}

func TestOperatorDelete_NoAuth(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register an operator so count > 0.
	regResp := registerOperatorHelper(t, mux, "Alice", "director")
	id := regResp["id"].(string)

	// Delete without any auth token — should get 401 since operators exist.
	req := httptest.NewRequest("DELETE", "/api/operator/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

// --- Invite token tests ---

func TestOperatorRegister_InviteToken_Success(t *testing.T) {
	tokens := map[string]string{
		"dir-tok-123": "director",
		"aud-tok-456": "audio",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	body := map[string]string{"name": "Alice", "inviteToken": "dir-tok-123"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "Alice", resp["name"])
	require.Equal(t, "director", resp["role"], "role should come from invite token, not request")
}

func TestOperatorRegister_InviteToken_OverridesRequestRole(t *testing.T) {
	tokens := map[string]string{
		"aud-tok-456": "audio",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	// Client sends role=director but invite token maps to audio.
	body := map[string]string{"name": "Bob", "role": "director", "inviteToken": "aud-tok-456"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "audio", resp["role"], "role should be overridden by invite token")
}

func TestOperatorRegister_InviteToken_InvalidToken(t *testing.T) {
	tokens := map[string]string{
		"dir-tok-123": "director",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	body := map[string]string{"name": "Eve", "inviteToken": "wrong-token"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "invalid invite token", resp["error"])
}

func TestOperatorRegister_InviteToken_MissingWhenRequired(t *testing.T) {
	tokens := map[string]string{
		"dir-tok-123": "director",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	// No inviteToken field at all.
	body := map[string]string{"name": "Eve", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "invite token required", resp["error"])
}

func TestOperatorRegister_NoInviteTokens_DemoMode(t *testing.T) {
	// Demo mode with no invite tokens: register is open, role from body.
	_, mux := setupOperatorTestAPI(t)

	resp := registerOperatorHelper(t, mux, "Alice", "director")
	require.Equal(t, "director", resp["role"])
}

func TestOperatorRegister_InviteTokens_SessionOwnerBypass(t *testing.T) {
	tokens := map[string]string{
		"aud-tok-456": "audio",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	body := map[string]string{"name": "Owner", "role": "director"}
	req := bearerRequest("POST", "/api/operator/register", body, inviteTokenSessionAPIToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "director", resp["role"],
		"session owner should register with the role in the body, bypassing invite-token role override")
}

// --- Invite tokens endpoint tests ---

func TestOperatorInviteTokens_DirectorSuccess(t *testing.T) {
	tokens := map[string]string{
		"dir-tok-123": "director",
		"aud-tok-456": "audio",
		"gfx-tok-789": "graphics",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	// Register a director using an invite token.
	body := map[string]string{"name": "Director", "inviteToken": "dir-tok-123"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var regResp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	dirToken := regResp["token"].(string)

	// GET invite tokens as director.
	req = bearerRequest("GET", "/api/operator/invite-tokens", nil, dirToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var result map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&result)
	require.Equal(t, "dir-tok-123", result["director"])
	require.Equal(t, "aud-tok-456", result["audio"])
	require.Equal(t, "gfx-tok-789", result["graphics"])
}

func TestOperatorInviteTokens_NonDirectorForbidden(t *testing.T) {
	tokens := map[string]string{
		"aud-tok-456": "audio",
	}
	_, mux := setupOperatorTestAPIWithInviteTokens(t, tokens)

	// Register an audio operator.
	body := map[string]string{"name": "AudioOp", "inviteToken": "aud-tok-456"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var regResp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&regResp)
	audioToken := regResp["token"].(string)

	// GET invite tokens as non-director — should be forbidden.
	req = bearerRequest("GET", "/api/operator/invite-tokens", nil, audioToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
}

func TestOperatorInviteTokens_NoAuth(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	req := httptest.NewRequest("GET", "/api/operator/invite-tokens", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
}

func TestOperatorInviteTokens_EmptyWhenNotConfigured(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// Register a director (no invite tokens = backward compat).
	regResp := registerOperatorHelper(t, mux, "Director", "director")
	dirToken := regResp["token"].(string)

	req := bearerRequest("GET", "/api/operator/invite-tokens", nil, dirToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var result map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&result)
	require.Empty(t, result, "should return empty map when no invite tokens configured")
}

// --- Client-generated ID tests (API level) ---

func TestOperatorRegister_WithClientID(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	clientID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	body := map[string]string{"id": clientID, "name": "Alice", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, clientID, resp["id"], "server should use the client-provided ID")
	require.Equal(t, "Alice", resp["name"])
	require.Equal(t, "director", resp["role"])
	require.Contains(t, resp, "token")
}

func TestOperatorRegister_WithoutClientID_BackwardCompat(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	// No "id" field — should generate one (backward compat).
	resp := registerOperatorHelper(t, mux, "Alice", "director")
	require.Contains(t, resp, "id")
	require.NotEmpty(t, resp["id"])
}

// --- Unauthenticated-register hardening (BUG-085) ---

func TestOperatorRegister_RejectsUnauthenticatedWhenNoInviteTokens(t *testing.T) {
	api, mux := setupOperatorTestAPIBare(t)

	body := map[string]string{"name": "evil", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t,
		"operator registration requires invite token or session token",
		resp["error"])

	require.Equal(t, 0, api.operatorStore.Count(),
		"no operator should be persisted when register is rejected")
}

func TestOperatorRegister_AcceptsSessionOwnerWhenNoInviteTokens(t *testing.T) {
	const sessionToken = "session-token-abcdef0123456789"
	api, mux := setupOperatorTestAPIWithSessionToken(t, sessionToken)

	body := map[string]string{"name": "Owner", "role": "director"}
	req := bearerRequest("POST", "/api/operator/register", body, sessionToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Equal(t, "Owner", resp["name"])
	require.Equal(t, "director", resp["role"])
	require.Contains(t, resp, "token")

	require.Equal(t, 1, api.operatorStore.Count(),
		"operator should be persisted when authenticated as session owner")
}

func TestOperatorRegister_RejectsInvalidRoleEvenWhenAuthed(t *testing.T) {
	const sessionToken = "session-token-abcdef0123456789"
	_, mux := setupOperatorTestAPIWithSessionToken(t, sessionToken)

	body := map[string]string{"name": "Bob", "role": "system"}
	req := bearerRequest("POST", "/api/operator/register", body, sessionToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.Contains(t, resp, "error")
}

func TestOperatorRegister_DuplicateClientID_Idempotent(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	clientID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	// First registration.
	body := map[string]string{"id": clientID, "name": "Alice", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "first register: %s", rec.Body.String())

	var resp1 map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp1)

	// Second registration with the same client ID (simulates dual-engine fan-out).
	b, _ = json.Marshal(body)
	req = httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "second register: %s", rec.Body.String())

	var resp2 map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp2)

	require.Equal(t, resp1["id"], resp2["id"], "both should return same ID")
	require.Equal(t, resp1["token"], resp2["token"], "both should return same token")
}
