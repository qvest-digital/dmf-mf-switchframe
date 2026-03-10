package operator

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointSubsystem(t *testing.T) {
	tests := []struct {
		path string
		sub  Subsystem
		ok   bool
	}{
		{"/api/switch/cut", SubsystemSwitching, true},
		{"/api/switch/preview", SubsystemSwitching, true},
		{"/api/switch/transition", SubsystemSwitching, true},
		{"/api/switch/ftb", SubsystemSwitching, true},
		{"/api/audio/level", SubsystemAudio, true},
		{"/api/audio/trim", SubsystemAudio, true},
		{"/api/audio/mute", SubsystemAudio, true},
		{"/api/audio/afv", SubsystemAudio, true},
		{"/api/audio/master", SubsystemAudio, true},
		{"/api/audio/cam1/eq", SubsystemAudio, true},
		{"/api/audio/cam1/compressor", SubsystemAudio, true},
		{"/api/graphics/on", SubsystemGraphics, true},
		{"/api/graphics/off", SubsystemGraphics, true},
		{"/api/graphics/auto-on", SubsystemGraphics, true},
		{"/api/graphics/auto-off", SubsystemGraphics, true},
		{"/api/replay/mark-in", SubsystemReplay, true},
		{"/api/replay/mark-out", SubsystemReplay, true},
		{"/api/replay/play", SubsystemReplay, true},
		{"/api/replay/stop", SubsystemReplay, true},
		{"/api/recording/start", SubsystemOutput, true},
		{"/api/recording/stop", SubsystemOutput, true},
		{"/api/output/srt/start", SubsystemOutput, true},
		{"/api/output/srt/stop", SubsystemOutput, true},
		{"/api/presets/abc/recall", SubsystemSwitching, true}, // preset recall
		{"/api/presets/abc", SubsystemSwitching, true},        // preset mutation (update/delete)
		{"/api/captions/mode", SubsystemCaptions, true},         // caption mode
		{"/api/captions/text", SubsystemCaptions, true},         // caption text
		{"/api/switch/state", "", false},                        // GET endpoint
		{"/api/operator/register", "", false},                   // operator management
		{"/api/presets", "", false},                             // list endpoint (no trailing slash)
		{"/api/unknown", "", false},
	}

	for _, tc := range tests {
		sub, ok := EndpointSubsystem(tc.path)
		require.Equal(t, tc.ok, ok, "EndpointSubsystem(%q): ok mismatch", tc.path)
		if ok {
			require.Equal(t, tc.sub, sub, "EndpointSubsystem(%q): subsystem mismatch", tc.path)
		}
	}
}

func TestMiddleware_NoOperatorsPassesThrough(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 with no operators")
}

func TestMiddleware_GETPassesThrough(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	// Register an operator so middleware is active.
	_, _ = store.Register("Alice", RoleDirector)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 for GET")
}

func TestMiddleware_OperatorEndpointsExempt(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	_, _ = store.Register("Alice", RoleDirector)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("POST", "/api/operator/register", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 for operator endpoint")
}

func TestMiddleware_RoleForbidden(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Bob", RoleAudio)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code, "expected 403 for audio role on switching")
}

func TestMiddleware_LockConflict(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	alice, _ := store.Register("Alice", RoleDirector)
	bob, _ := store.Register("Bob", RoleDirector)
	sm.Connect(alice.ID, alice.Name, alice.Role)
	sm.Connect(bob.ID, bob.Name, bob.Role)

	// Alice locks switching.
	_ = sm.AcquireLock(alice.ID, SubsystemSwitching)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Bob tries to cut — should get 409.
	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.Header.Set("Authorization", "Bearer "+bob.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusConflict, rr.Code, "expected 409 for locked subsystem")
}

func TestMiddleware_LockOwnerAllowed(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	alice, _ := store.Register("Alice", RoleDirector)
	sm.Connect(alice.ID, alice.Name, alice.Role)
	_ = sm.AcquireLock(alice.ID, SubsystemSwitching)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Alice (lock owner) should be allowed.
	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.Header.Set("Authorization", "Bearer "+alice.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 for lock owner")
}

func TestMiddleware_CaptionerAllowedOnCaptions(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Carol", RoleCaptioner)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("POST", "/api/captions/text", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 for captioner on captions")
}

func TestMiddleware_CaptionerForbiddenOnSwitching(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Carol", RoleCaptioner)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	req := httptest.NewRequest("POST", "/api/switch/cut", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code, "expected 403 for captioner on switching")
}

func TestCanCommand_Captioner(t *testing.T) {
	require.True(t, CanCommand(RoleCaptioner, SubsystemCaptions))
	require.False(t, CanCommand(RoleCaptioner, SubsystemSwitching))
	require.False(t, CanCommand(RoleCaptioner, SubsystemAudio))
	require.True(t, CanCommand(RoleDirector, SubsystemCaptions))
}

func TestCanLock_Captioner(t *testing.T) {
	require.True(t, CanLock(RoleCaptioner, SubsystemCaptions))
	require.False(t, CanLock(RoleCaptioner, SubsystemSwitching))
	require.True(t, CanLock(RoleDirector, SubsystemCaptions))
}

func TestMiddleware_UnknownEndpointPassesThrough(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Alice", RoleViewer)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Unknown endpoint should pass through (not a lockable subsystem).
	req := httptest.NewRequest("POST", "/api/presets", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "expected 200 for unknown endpoint")
}
