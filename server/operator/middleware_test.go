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
		// Switching (exact matches)
		{"/api/switch/cut", SubsystemSwitching, true},
		{"/api/switch/preview", SubsystemSwitching, true},
		{"/api/switch/transition", SubsystemSwitching, true},
		{"/api/switch/ftb", SubsystemSwitching, true},

		// Audio (exact + prefix)
		{"/api/audio/level", SubsystemAudio, true},
		{"/api/audio/trim", SubsystemAudio, true},
		{"/api/audio/mute", SubsystemAudio, true},
		{"/api/audio/afv", SubsystemAudio, true},
		{"/api/audio/master", SubsystemAudio, true},
		{"/api/audio/cam1/eq", SubsystemAudio, true},
		{"/api/audio/cam1/compressor", SubsystemAudio, true},

		// Graphics with layer ID (bug: exact paths /api/graphics/on never match)
		{"/api/graphics/3/on", SubsystemGraphics, true},
		{"/api/graphics/0/off", SubsystemGraphics, true},
		{"/api/graphics/1/auto-on", SubsystemGraphics, true},
		{"/api/graphics/2/auto-off", SubsystemGraphics, true},
		{"/api/graphics/5/frame", SubsystemGraphics, true},
		{"/api/graphics/1/animate", SubsystemGraphics, true},
		{"/api/graphics/0/fly-in", SubsystemGraphics, true},
		{"/api/graphics/0/fly-out", SubsystemGraphics, true},
		{"/api/graphics/0/slide", SubsystemGraphics, true},
		{"/api/graphics/1/rect", SubsystemGraphics, true},
		{"/api/graphics/1/zorder", SubsystemGraphics, true},
		{"/api/graphics/2/image", SubsystemGraphics, true},
		{"/api/graphics/1/ticker", SubsystemGraphics, true},
		{"/api/graphics/1/text-animate", SubsystemGraphics, true},

		// Replay (exact matches)
		{"/api/replay/mark-in", SubsystemReplay, true},
		{"/api/replay/mark-out", SubsystemReplay, true},
		{"/api/replay/play", SubsystemReplay, true},
		{"/api/replay/stop", SubsystemReplay, true},

		// Output (exact matches)
		{"/api/recording/start", SubsystemOutput, true},
		{"/api/recording/stop", SubsystemOutput, true},
		{"/api/output/srt/start", SubsystemOutput, true},
		{"/api/output/srt/stop", SubsystemOutput, true},

		// Presets (prefix)
		{"/api/presets/abc/recall", SubsystemSwitching, true},
		{"/api/presets/abc", SubsystemSwitching, true},

		// Captions (prefix)
		{"/api/captions/mode", SubsystemCaptions, true},
		{"/api/captions/text", SubsystemCaptions, true},

		// Layout/PIP (unmapped — should be switching)
		{"/api/layout", SubsystemSwitching, true},
		{"/api/layout/slots/0/on", SubsystemSwitching, true},
		{"/api/layout/slots/1/off", SubsystemSwitching, true},
		{"/api/layout/slots/2/source", SubsystemSwitching, true},
		{"/api/layout/presets", SubsystemSwitching, true},

		// Destinations (unmapped — should be output)
		{"/api/output/destinations", SubsystemOutput, true},
		{"/api/output/destinations/d1", SubsystemOutput, true},
		{"/api/output/destinations/d1/start", SubsystemOutput, true},
		{"/api/output/destinations/d1/stop", SubsystemOutput, true},

		// Clips (unmapped — should be switching)
		{"/api/clips/upload", SubsystemSwitching, true},
		{"/api/clips/abc123", SubsystemSwitching, true},
		{"/api/clips/players/1/load", SubsystemSwitching, true},
		{"/api/clips/players/1/play", SubsystemSwitching, true},
		{"/api/clips/players/1/stop", SubsystemSwitching, true},
		{"/api/clips/from-recording", SubsystemSwitching, true},

		// SCTE-35 (unmapped — should be output)
		{"/api/scte35/cue", SubsystemOutput, true},
		{"/api/scte35/return", SubsystemOutput, true},
		{"/api/scte35/return/42", SubsystemOutput, true},
		{"/api/scte35/cancel/42", SubsystemOutput, true},
		{"/api/scte35/hold/42", SubsystemOutput, true},
		{"/api/scte35/extend/42", SubsystemOutput, true},
		{"/api/scte35/rules", SubsystemOutput, true},

		// Stinger (unmapped — should be graphics)
		{"/api/stinger/list", SubsystemGraphics, true},
		{"/api/stinger/my-wipe/upload", SubsystemGraphics, true},
		{"/api/stinger/my-wipe", SubsystemGraphics, true},
		{"/api/stinger/my-wipe/cut-point", SubsystemGraphics, true},

		// Format (unmapped — should be switching)
		{"/api/format", SubsystemSwitching, true},

		// Encoder (unmapped — should be switching)
		{"/api/encoder", SubsystemSwitching, true},

		// Exempt / unmapped (should return false)
		{"/api/switch/state", "", false},
		{"/api/operator/register", "", false},
		{"/api/presets", "", false}, // list endpoint (no trailing slash)
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
