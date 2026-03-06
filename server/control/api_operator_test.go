package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/switcher"
)

// setupOperatorTestAPI creates a test API with operator store and session manager.
// The switcher is minimal (no sources) but non-nil to avoid panics in NewAPI.
func setupOperatorTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sm := operator.NewSessionManager()
	t.Cleanup(sm.Close)

	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)

	api := NewAPI(sw, WithOperatorStore(store), WithSessionManager(sm))

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
	if rec.Code != http.StatusOK {
		t.Fatalf("register %s: expected 200, got %d: %s", name, rec.Code, rec.Body.String())
	}
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
	if _, ok := resp["id"]; !ok {
		t.Error("response missing 'id' field")
	}
	if name, _ := resp["name"].(string); name != "Alice" {
		t.Errorf("name = %q, want %q", name, "Alice")
	}
	if role, _ := resp["role"].(string); role != "director" {
		t.Errorf("role = %q, want %q", role, "director")
	}
	if _, ok := resp["token"]; !ok {
		t.Error("response missing 'token' field")
	}
}

func TestOperatorRegister_EmptyName(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	body := map[string]string{"name": "", "role": "director"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
}

func TestOperatorRegister_InvalidRole(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	body := map[string]string{"name": "Bob", "role": "superadmin"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/operator/register", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if name, _ := resp["name"].(string); name != "Alice" {
		t.Errorf("name = %q, want %q", name, "Alice")
	}
	if role, _ := resp["role"].(string); role != "director" {
		t.Errorf("role = %q, want %q", role, "director")
	}
}

func TestOperatorReconnect_InvalidToken(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	req := bearerRequest("POST", "/api/operator/reconnect", nil, "bogus-token-12345")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
}

// --- Heartbeat tests ---

func TestOperatorHeartbeat_Success(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	regResp := registerOperatorHelper(t, mux, "Alice", "director")
	token := regResp["token"].(string)

	req := bearerRequest("POST", "/api/operator/heartbeat", nil, token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["ok"] {
		t.Error("expected ok=true in response")
	}
}

func TestOperatorHeartbeat_InvalidToken(t *testing.T) {
	_, mux := setupOperatorTestAPI(t)

	req := bearerRequest("POST", "/api/operator/heartbeat", nil, "bogus-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var result []operator.OperatorInfo
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d operators, want 2", len(result))
	}

	// Both should be connected since register auto-connects.
	for _, op := range result {
		if !op.Connected {
			t.Errorf("operator %q: expected connected=true", op.Name)
		}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["ok"] {
		t.Error("expected ok=true in response")
	}
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
	if rec.Code != http.StatusOK {
		t.Fatalf("first lock: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Director tries to lock the same subsystem — should get 409 conflict.
	req = bearerRequest("POST", "/api/operator/lock",
		map[string]string{"subsystem": "audio"}, directorToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
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
	if rec.Code != http.StatusOK {
		t.Fatalf("lock: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Unlock.
	req = bearerRequest("POST", "/api/operator/unlock",
		map[string]string{"subsystem": "audio"}, token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["ok"] {
		t.Error("expected ok=true in response")
	}
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
	if rec.Code != http.StatusOK {
		t.Fatalf("lock: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Non-director (audio operator) tries to force-unlock — should fail 403.
	req = bearerRequest("POST", "/api/operator/force-unlock",
		map[string]string{"subsystem": "audio"}, audioToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-director force-unlock: status = %d, want %d; body: %s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var errResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if _, ok := errResp["error"]; !ok {
		t.Error("response missing 'error' field")
	}

	// Director force-unlocks — should succeed.
	req = bearerRequest("POST", "/api/operator/force-unlock",
		map[string]string{"subsystem": "audio"}, dirToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("director force-unlock: status = %d, want %d; body: %s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["ok"] {
		t.Error("expected ok=true in response")
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
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

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
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

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("response missing 'error' field")
	}
}
