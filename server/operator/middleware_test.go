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
		{"/api/switch/ftb-duration", SubsystemSwitching, true},
		{"/api/switch/transition-abort-threshold", SubsystemSwitching, true},

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

		// Presets (prefix + bare collection root)
		{"/api/presets/abc/recall", SubsystemSwitching, true},
		{"/api/presets/abc", SubsystemSwitching, true},
		{"/api/presets", SubsystemSwitching, true}, // POST creates a preset

		// Collection-root mutating routes must map to their subtree subsystem
		// (regression: bare paths used to miss the trailing-slash prefix and
		// fall through to deny-by-default, bypassing per-subsystem role/lock).
		{"/api/graphics", SubsystemGraphics, true},    // POST adds a layer
		{"/api/sources", SubsystemSwitching, true},    // POST creates a source
		{"/api/colorgrade", SubsystemSwitching, true}, // PUT alters program grade

		// Captions (prefix)
		{"/api/captions/mode", SubsystemCaptions, true},
		{"/api/captions/text", SubsystemCaptions, true},

		// DVE (switching subsystem)
		{"/api/dve", SubsystemSwitching, true},
		{"/api/dve/slots/0", SubsystemSwitching, true},
		{"/api/dve/slots/1/on", SubsystemSwitching, true},
		{"/api/dve/slots/2/off", SubsystemSwitching, true},
		{"/api/dve/presets", SubsystemSwitching, true},

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
		{"/api/sync/state-snapshot", "", false}, // unmapped; authenticated-operator carve-out
		{"/api/peer/force-leader", "", false},   // unmapped; machine-auth carve-out
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
		{"/api/v1/dve/slots/0/on", SubsystemSwitching, true},
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

func TestMiddleware_UnmappedMutatingEndpointDeniedForNonDirector(t *testing.T) {
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

	// POST /api/factory-reset is an unmapped mutating path with no explicit
	// carve-out. Under the deny-by-default policy a non-director must NOT
	// reach the handler.
	req := httptest.NewRequest("POST", "/api/factory-reset", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code,
		"expected 403 for viewer on unmapped mutating endpoint")
}

func TestMiddleware_UnmappedMutatingEndpointAllowedForDirector(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	op, _ := store.Register("Alice", RoleDirector)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	// Director retains access to unmapped privileged mutating endpoints.
	req := httptest.NewRequest("POST", "/api/factory-reset", nil)
	req.Header.Set("Authorization", "Bearer "+op.Token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code,
		"expected 200 for director on unmapped mutating endpoint")
}

// TestMiddleware_ViewerForbiddenOnProgramAffectingEndpoints is the direct
// regression test for the operator-authz finding: a read-only viewer must not
// be able to drive on-air playout, color grade, ST-map program geometry,
// pipeline-node bypass, or apply a full engine state snapshot. Before the fix
// every one of these unmapped mutating paths fell through to a permissive
// branch and returned 200 for any registered operator.
func TestMiddleware_ViewerForbiddenOnProgramAffectingEndpoints(t *testing.T) {
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

	cases := []struct {
		method string
		path   string
	}{
		// Playout on-air automation (channels + pods).
		{"POST", "/api/playout/channels/1/start"},
		{"POST", "/api/playout/channels/1/stop"},
		{"POST", "/api/playout/channels/1/jump"},
		{"POST", "/api/playout/channels/1/load"},
		{"POST", "/api/playout/channels/1/pods/p1/arm"},
		{"POST", "/api/playout/channels/1/pods/p1/fire"},
		{"POST", "/api/playout/channels/1/pods/p1/dump"},
		// Color grade (alters program color).
		{"PUT", "/api/colorgrade"},
		{"PUT", "/api/colorgrade/corrector"},
		{"DELETE", "/api/colorgrade/corrector"},
		{"POST", "/api/colorgrade/upload"},
		{"DELETE", "/api/colorgrade/my-look"},
		// ST-map program + source geometry.
		{"PUT", "/api/stmap/program"},
		{"DELETE", "/api/stmap/program"},
		{"PUT", "/api/stmap/source/cam1"},
		{"POST", "/api/stmap/upload/foo"},
		// Pipeline node bypass (DSK/color/AI on program).
		{"PUT", "/api/pipeline/nodes/dsk/bypass"},
		{"DELETE", "/api/pipeline/nodes/dsk/bypass"},
		// ASR config (drives captions).
		{"PUT", "/api/asr/config"},
		// Custom wipe upload/delete.
		{"POST", "/api/wipes/custom"},
		{"DELETE", "/api/wipes/custom/w1"},
		// Replay shutter angle.
		{"PUT", "/api/replay/shutter-angle"},
		// NOTE: /api/sync/state-snapshot is intentionally absent here. The
		// dual-engine auto-reconcile loop runs in every operator's browser
		// (including viewers), so it is carved out for any authenticated
		// registered operator — see TestMiddleware_SyncEndpoints*.
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+op.Token)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code,
			"expected 403 for viewer on %s %s", tc.method, tc.path)
	}
}

// TestMiddleware_CollectionRootRoutesGuarded is the direct regression test for
// the collection-root finding: mutating routes on a collection root
// (POST /api/graphics, POST /api/sources, POST /api/presets) previously missed
// the trailing-slash prefix match and fell through to deny-by-default, so they
// bypassed BOTH the per-subsystem role check (CanCommand) and the lock check
// (CheckLock). They must now be guarded by the same subsystem as their subtree.
func TestMiddleware_CollectionRootRoutesGuarded(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// route -> the subsystem it must be guarded under.
	routes := []struct {
		method string
		path   string
		sub    Subsystem
	}{
		{"POST", "/api/graphics", SubsystemGraphics},   // handleGraphicsAddLayer
		{"POST", "/api/sources", SubsystemSwitching},   // handleCreateSource
		{"POST", "/api/presets", SubsystemSwitching},   // handleCreatePreset
		{"PUT", "/api/colorgrade", SubsystemSwitching}, // handleColorGradeSet
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			store, _ := NewStore(tempStorePath(t))
			sm := NewSessionManager()
			defer sm.Close()

			director, _ := store.Register("Alice", RoleDirector)
			viewer, _ := store.Register("Viewer", RoleViewer)
			// An operator whose role canNOT command rt.sub: audio is wrong for
			// both switching and graphics.
			audio, _ := store.Register("Bob", RoleAudio)
			sm.Connect(director.ID, director.Name, director.Role)
			sm.Connect(viewer.ID, viewer.Name, viewer.Role)
			sm.Connect(audio.ID, audio.Name, audio.Role)

			mw := NewMiddleware(store, sm)
			wrapped := mw(handler)

			do := func(token string) int {
				req := httptest.NewRequest(rt.method, rt.path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rr := httptest.NewRecorder()
				wrapped.ServeHTTP(rr, req)
				return rr.Code
			}

			// Role check is enforced: a viewer and a wrong-role operator are
			// forbidden (not silently allowed via the fall-through branch).
			require.Equal(t, http.StatusForbidden, do(viewer.Token),
				"viewer must be forbidden on %s %s", rt.method, rt.path)
			require.Equal(t, http.StatusForbidden, do(audio.Token),
				"audio role must be forbidden on %s %s", rt.method, rt.path)

			// A director (commands all subsystems) is allowed when unlocked.
			require.Equal(t, http.StatusOK, do(director.Token),
				"director must be allowed on %s %s", rt.method, rt.path)

			// Lock check is enforced: once the director locks the subsystem,
			// a second director is blocked with 409 — proving these routes go
			// through CheckLock rather than the unconditional pass-through.
			second, _ := store.Register("Carol", RoleDirector)
			sm.Connect(second.ID, second.Name, second.Role)
			require.NoError(t, sm.AcquireLock(director.ID, rt.sub))
			require.Equal(t, http.StatusConflict, do(second.Token),
				"second director must get 409 while %s holds the %s lock", director.Name, rt.sub)
		})
	}
}

// TestMiddleware_SyncEndpointsAllowAnyAuthenticatedOperator verifies the
// dual-engine reconciliation endpoints are callable by every registered
// operator role, not just directors. The auto-reconcile loop runs in every
// operator's browser (the UI wires it with no role gating), so gating these
// behind the director role would break engine reconciliation whenever no
// director has a browser open.
func TestMiddleware_SyncEndpointsAllowAnyAuthenticatedOperator(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	roles := []Role{RoleDirector, RoleAudio, RoleGraphics, RoleCaptioner, RoleViewer}
	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/sync/state-snapshot"},
		{"POST", "/api/sync/reconcile-lock"},
		{"PUT", "/api/sync/reconcile-lock/lock-123"},
		{"DELETE", "/api/sync/reconcile-lock/lock-123"},
		// Versioned variants must be classified identically.
		{"POST", "/api/v1/sync/state-snapshot"},
		{"POST", "/api/v1/sync/reconcile-lock"},
	}

	for _, role := range roles {
		op, err := store.Register("op-"+string(role), role)
		require.NoError(t, err)
		sm.Connect(op.ID, op.Name, op.Role)

		for _, ep := range endpoints {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.Header.Set("Authorization", "Bearer "+op.Token)
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code,
				"expected 200 for role %q on %s %s", role, ep.method, ep.path)
		}
	}
}

// TestMiddleware_SyncEndpointsRejectUnidentifiedCallers verifies the sync
// carve-out still requires an identified registered operator: missing or
// unknown bearer tokens are rejected. This is stricter than main, which let
// these endpoints through with no operator identity at all.
func TestMiddleware_SyncEndpointsRejectUnidentifiedCallers(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	// Register an operator so the middleware is active.
	op, _ := store.Register("Alice", RoleDirector)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/sync/state-snapshot"},
		{"POST", "/api/sync/reconcile-lock"},
		{"PUT", "/api/sync/reconcile-lock/lock-123"},
		{"DELETE", "/api/sync/reconcile-lock/lock-123"},
	}

	for _, ep := range endpoints {
		// No token at all.
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code,
			"expected 403 with no token on %s %s", ep.method, ep.path)

		// Unknown token.
		req = httptest.NewRequest(ep.method, ep.path, nil)
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		rr = httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code,
			"expected 403 with unknown token on %s %s", ep.method, ep.path)
	}
}

// TestMiddleware_ForceLeaderMachineAuthExempt verifies POST
// /api/peer/force-leader bypasses the operator role middleware even when
// operators are registered. The canonical caller is the cloud control plane,
// which authenticates via the X-Control-Plane-Secret header (enforced inside
// the handler, control/api_force_leader.go) and never holds an operator
// bearer token. Requiring a director token here would make failover promotion
// impossible during a partition.
func TestMiddleware_ForceLeaderMachineAuthExempt(t *testing.T) {
	store, _ := NewStore(tempStorePath(t))
	sm := NewSessionManager()
	defer sm.Close()

	// Register operators so the middleware is active (the regression only
	// manifests once Count() > 0).
	op, _ := store.Register("Alice", RoleDirector)
	sm.Connect(op.ID, op.Name, op.Role)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := NewMiddleware(store, sm)
	wrapped := mw(handler)

	for _, path := range []string{"/api/peer/force-leader", "/api/v1/peer/force-leader"} {
		// No Authorization header — exactly how the control plane calls it.
		req := httptest.NewRequest("POST", path, nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code,
			"expected 200 (pass-through to secret-checking handler) for POST %s with no bearer token", path)
	}
}
