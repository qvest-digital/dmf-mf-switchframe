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
		{"/api/replay/quick", SubsystemReplay, true},
		{"/api/replay/pause", SubsystemReplay, true},
		{"/api/replay/resume", SubsystemReplay, true},
		{"/api/replay/seek", SubsystemReplay, true},
		{"/api/replay/speed", SubsystemReplay, true},
		{"/api/replay/marks", SubsystemReplay, true},

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

		// Comms — no lockable subsystem, should pass through
		{"/api/comms/join", "", false},
		{"/api/comms/leave", "", false},
		{"/api/comms/mute", "", false},

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

func TestEndpointSubsystem_V1Prefix(t *testing.T) {
	// /api/v1/ paths should map to the same subsystem as their /api/ equivalents.
	tests := []struct {
		path string
		sub  Subsystem
		ok   bool
	}{
		{"/api/v1/switch/cut", SubsystemSwitching, true},
		{"/api/v1/switch/preview", SubsystemSwitching, true},
		{"/api/v1/audio/level", SubsystemAudio, true},
		{"/api/v1/audio/cam1/eq", SubsystemAudio, true},
		{"/api/v1/graphics/3/on", SubsystemGraphics, true},
		{"/api/v1/replay/play", SubsystemReplay, true},
		{"/api/v1/replay/quick", SubsystemReplay, true},
		{"/api/v1/replay/pause", SubsystemReplay, true},
		{"/api/v1/replay/resume", SubsystemReplay, true},
		{"/api/v1/replay/seek", SubsystemReplay, true},
		{"/api/v1/replay/speed", SubsystemReplay, true},
		{"/api/v1/replay/marks", SubsystemReplay, true},
		{"/api/v1/recording/start", SubsystemOutput, true},
		{"/api/v1/layout/slots/0/on", SubsystemSwitching, true},
		{"/api/v1/scte35/cue", SubsystemOutput, true},
		{"/api/v1/clips/upload", SubsystemSwitching, true},
		{"/api/v1/captions/text", SubsystemCaptions, true},
		// Exempt paths should still return false.
		{"/api/v1/switch/state", "", false},
		{"/api/v1/unknown", "", false},
	}

	for _, tc := range tests {
		sub, ok := EndpointSubsystem(tc.path)
		require.Equal(t, tc.ok, ok, "EndpointSubsystem(%q): ok mismatch", tc.path)
		if ok {
			require.Equal(t, tc.sub, sub, "EndpointSubsystem(%q): subsystem mismatch", tc.path)
		}
	}
}

func TestMiddleware_V1PrefixEnforcesPermissions(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	// Register an audio operator — should NOT be able to cut.
	op, _ := store.Register("Bob", RoleAudio)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// POST /api/v1/switch/cut should be blocked for audio role, not bypassed.
	req := httptest.NewRequest("POST", "/api/v1/switch/cut", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code,
		"expected 403 for audio role on /api/v1/switch/cut, got %d (permission bypass via v1 prefix)", rr.Code)
}

func TestMiddleware_ReplayTransportEndpointsEnforceLock(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	alice, _ := store.Register("Alice", RoleDirector)
	bob, _ := store.Register("Bob", RoleDirector)
	sm.Connect(alice.ID, alice.Name, alice.Role)
	sm.Connect(bob.ID, bob.Name, bob.Role)

	// Alice locks replay.
	_ = sm.AcquireLock(alice.ID, SubsystemReplay)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// All replay transport endpoints should be blocked for Bob.
	replayEndpoints := []string{
		"/api/replay/quick",
		"/api/replay/pause",
		"/api/replay/resume",
		"/api/replay/seek",
		"/api/replay/speed",
		"/api/replay/marks",
	}

	for _, ep := range replayEndpoints {
		req := httptest.NewRequest("POST", ep, nil)
		req.Header.Set("Authorization", "Bearer "+bob.Token)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusConflict, rr.Code,
			"expected 409 for Bob on %s (replay locked by Alice)", ep)
	}

	// Alice (lock owner) should be allowed on all of them.
	for _, ep := range replayEndpoints {
		req := httptest.NewRequest("POST", ep, nil)
		req.Header.Set("Authorization", "Bearer "+alice.Token)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code,
			"expected 200 for Alice (lock owner) on %s", ep)
	}
}

func TestMiddleware_ViewerForbiddenOnReplayTransport(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Viewer", RoleViewer)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Viewer role should be forbidden from replay endpoints.
	replayEndpoints := []string{
		"/api/replay/quick",
		"/api/replay/pause",
		"/api/replay/resume",
		"/api/replay/seek",
		"/api/replay/speed",
		"/api/replay/marks",
	}

	for _, ep := range replayEndpoints {
		req := httptest.NewRequest("POST", ep, nil)
		req.Header.Set("Authorization", "Bearer "+op.Token)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code,
			"expected 403 for viewer on %s", ep)
	}
}

func TestMiddleware_CommsPassesThrough(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	// Register a viewer — most restricted role.
	op, _ := store.Register("Viewer", RoleViewer)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Comms endpoints have no lockable subsystem, so even a viewer can use them.
	commsEndpoints := []string{
		"/api/comms/join",
		"/api/comms/leave",
		"/api/comms/mute",
	}

	for _, ep := range commsEndpoints {
		req := httptest.NewRequest("POST", ep, nil)
		req.Header.Set("Authorization", "Bearer "+op.Token)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code,
			"expected 200 for comms endpoint %s (no lockable subsystem)", ep)
	}
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
