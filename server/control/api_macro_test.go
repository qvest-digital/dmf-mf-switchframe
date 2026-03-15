package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
)

// setupMacroTestAPI creates an API with a macro store backed by a temp file.
// The switcher is nil since macro endpoints (dismiss, cancel, run with wait-only
// steps) never touch the switcher.
func setupMacroTestAPI(t *testing.T) (*API, *macro.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := macro.NewStore(filepath.Join(dir, "macros.json"))
	require.NoError(t, err)
	api := NewAPI(nil, WithMacroStore(store))
	return api, store
}

func TestHandleDismissMacro(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	var broadcastCount atomic.Int32
	api.SetBroadcastFunc(func() {
		broadcastCount.Add(1)
	})

	req := httptest.NewRequest("DELETE", "/api/macros/execution", nil)
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.GreaterOrEqual(t, broadcastCount.Load(), int32(1), "broadcast should have been called")
}

func TestHandleDismissMacro_Idempotent(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// First dismiss — nothing running, should be fine.
	req := httptest.NewRequest("DELETE", "/api/macros/execution", nil)
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// Second dismiss — still nothing, still 204.
	req = httptest.NewRequest("DELETE", "/api/macros/execution", nil)
	rec = httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHandleCancelMacro_NothingRunning(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	req := httptest.NewRequest("POST", "/api/macros/execution/cancel", nil)
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleRunMacro_Returns202Immediately(t *testing.T) {
	api, store := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// Save a macro with a long wait step — the handler should return before
	// the macro completes.
	err := store.Save(macro.Macro{
		Name: "long-wait",
		Steps: []macro.Step{
			{Action: macro.ActionWait, Params: map[string]any{"ms": float64(5000)}},
		},
	})
	require.NoError(t, err)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/macros/long-wait/run", strings.NewReader(body))
	rec := httptest.NewRecorder()

	start := time.Now()
	api.mux.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	// Handler should return almost immediately, not block for 5 seconds.
	require.Less(t, elapsed, 1*time.Second, "handler should return immediately, not block on macro execution")
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Response body should indicate the macro was started.
	var resp map[string]string
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "started", resp["status"])
	require.Equal(t, "long-wait", resp["name"])

	// Macro should be running in the background.
	api.macroMu.Lock()
	running := api.macroState != nil && api.macroState.Running
	cancel := api.macroCancel
	api.macroMu.Unlock()
	require.True(t, running, "macro should be running in background")

	// Clean up: cancel the running macro.
	require.NotNil(t, cancel)
	cancel()

	// Wait for the background goroutine to finish.
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroState == nil || !api.macroState.Running
	}, 5*time.Second, 50*time.Millisecond, "macro should complete after cancel")
}

func TestHandleRunMacro_ConcurrentRun(t *testing.T) {
	api, store := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// Save a macro with a long wait step so the first run blocks in background.
	err := store.Save(macro.Macro{
		Name: "long-wait",
		Steps: []macro.Step{
			{Action: macro.ActionWait, Params: map[string]any{"ms": float64(5000)}},
		},
	})
	require.NoError(t, err)

	// First run — returns 202 immediately, macro runs in background.
	body := `{}`
	req := httptest.NewRequest("POST", "/api/macros/long-wait/run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Wait for background goroutine to set macroState.Running.
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroState != nil && api.macroState.Running
	}, 2*time.Second, 10*time.Millisecond, "macro should be running")

	// Second run while first is still running should get 409 Conflict.
	req = httptest.NewRequest("POST", "/api/macros/long-wait/run", strings.NewReader(body))
	rec = httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

	// Clean up: cancel the running macro so the background goroutine exits.
	api.macroMu.Lock()
	cancel := api.macroCancel
	api.macroMu.Unlock()
	require.NotNil(t, cancel, "macroCancel should be set while macro is running")
	cancel()

	// Wait for the background goroutine to finish.
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroState == nil || !api.macroState.Running
	}, 5*time.Second, 50*time.Millisecond, "macro should finish after cancel")
}

func TestHandleRunMacro_BackgroundCompletionBroadcasts(t *testing.T) {
	api, store := setupMacroTestAPI(t)

	var broadcastCount atomic.Int32
	api.SetBroadcastFunc(func() {
		broadcastCount.Add(1)
	})

	// Save a macro with a very short wait so it completes quickly.
	err := store.Save(macro.Macro{
		Name: "quick",
		Steps: []macro.Step{
			{Action: macro.ActionWait, Params: map[string]any{"ms": float64(10)}},
		},
	})
	require.NoError(t, err)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/macros/quick/run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Wait for the macro to complete in the background.
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroState != nil && !api.macroState.Running
	}, 5*time.Second, 50*time.Millisecond, "macro should complete")

	// At least one broadcast should have been called (progress + completion).
	require.GreaterOrEqual(t, broadcastCount.Load(), int32(1), "broadcast should have been called on completion")

	// macroCancel should be cleared after completion.
	api.macroMu.Lock()
	cancel := api.macroCancel
	api.macroMu.Unlock()
	require.Nil(t, cancel, "macroCancel should be nil after completion")
}

func TestHandleDismissMacro_CancelsRunningExecution(t *testing.T) {
	api, store := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// Save a macro with a long wait step.
	err := store.Save(macro.Macro{
		Name: "slow",
		Steps: []macro.Step{
			{Action: macro.ActionWait, Params: map[string]any{"ms": float64(30000)}},
		},
	})
	require.NoError(t, err)

	// Run the macro.
	body := `{}`
	req := httptest.NewRequest("POST", "/api/macros/slow/run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Wait for it to start running.
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroState != nil && api.macroState.Running
	}, 2*time.Second, 20*time.Millisecond)

	// Dismiss (not cancel) — should also stop the macro.
	req = httptest.NewRequest("DELETE", "/api/macros/execution", nil)
	rec = httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// The macro should stop within a reasonable time (not run for 30 seconds).
	require.Eventually(t, func() bool {
		api.macroMu.Lock()
		defer api.macroMu.Unlock()
		return api.macroCancel == nil
	}, 2*time.Second, 50*time.Millisecond, "dismiss should cancel the running macro")
}

func TestBroadcastFn_RaceFree(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	// Concurrently set and call broadcast to verify no data race.
	var wg sync.WaitGroup
	var callCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			api.SetBroadcastFunc(func() {
				callCount.Add(1)
			})
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			api.broadcast()
		}()
	}

	wg.Wait()
	// Just verifying no race — exact count doesn't matter.
}

func TestClipMacroExecutor_UsesPlayerNotPlayerId(t *testing.T) {
	// The validator checks params["player"], so the executor must also read
	// "player" (not "playerId"). This test verifies the param key matches
	// by passing player=5 (invalid) and asserting ErrInvalidPlayer.
	// If the executor reads "playerId" instead, it defaults to 1 (valid)
	// and the error would be "clip not found" — not ErrInvalidPlayer.

	dir := t.TempDir()
	store, err := clip.NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := clip.NewManager(store, clip.ManagerConfig{})
	target := &apiMacroTarget{clipMgr: mgr}

	// clip_load with player=5 (out of range 1-4): should get ErrInvalidPlayer.
	params := map[string]any{
		"player": float64(5),
		"clipId": "test-clip",
	}
	err = target.Execute(context.Background(), "clip_load", params)
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipLoad must read 'player' key (not 'playerId'); got: %v", err)

	// clip_play with player=5: same assertion.
	err = target.Execute(context.Background(), "clip_play", map[string]any{
		"player": float64(5),
	})
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipPlay must read 'player' key; got: %v", err)

	// clip_pause with player=5.
	err = target.Execute(context.Background(), "clip_pause", map[string]any{
		"player": float64(5),
	})
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipPause must read 'player' key; got: %v", err)

	// clip_stop with player=5.
	err = target.Execute(context.Background(), "clip_stop", map[string]any{
		"player": float64(5),
	})
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipStop must read 'player' key; got: %v", err)

	// clip_eject with player=5.
	err = target.Execute(context.Background(), "clip_eject", map[string]any{
		"player": float64(5),
	})
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipEject must read 'player' key; got: %v", err)

	// clip_seek with player=5.
	err = target.Execute(context.Background(), "clip_seek", map[string]any{
		"player":   float64(5),
		"position": float64(0.5),
	})
	require.ErrorIs(t, err, clip.ErrInvalidPlayer,
		"execClipSeek must read 'player' key; got: %v", err)
}

func TestEnrichFn_RaceFree(t *testing.T) {
	// This test requires a non-nil switcher to call enrichedState.
	// We test the atomic operations directly instead.
	api, _ := setupMacroTestAPI(t)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			api.SetEnrichFunc(func(s internal.ControlRoomState) internal.ControlRoomState {
				return s
			})
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Read the enrichFn atomically — this is the race-prone path.
			fn := api.enrichFn.Load()
			_ = fn
		}()
	}

	wg.Wait()
}

func TestMacroState_ReturnsDeepCopy(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	// Set up a macro state with steps.
	api.macroMu.Lock()
	api.macroState = &internal.MacroExecutionState{
		Running:     true,
		MacroName:   "test-macro",
		CurrentStep: 1,
		Steps: []internal.MacroStepState{
			{Action: "cut", Summary: "Cut to camera2", Status: "done"},
			{Action: "wait", Summary: "Wait 500ms", Status: "running"},
		},
	}
	api.macroMu.Unlock()

	// Get the copy.
	copy1 := api.MacroState()
	require.NotNil(t, copy1)
	require.Equal(t, "test-macro", copy1.MacroName)
	require.True(t, copy1.Running)
	require.Len(t, copy1.Steps, 2)

	// Mutate the copy and verify the original is unchanged.
	copy1.Running = false
	copy1.Steps[0].Status = "MUTATED"

	api.macroMu.Lock()
	require.True(t, api.macroState.Running, "original Running should not be mutated by copy")
	require.Equal(t, "done", api.macroState.Steps[0].Status, "original Steps should not be mutated by copy")
	api.macroMu.Unlock()
}

func TestMacroState_NilReturnsNil(t *testing.T) {
	api, _ := setupMacroTestAPI(t)
	require.Nil(t, api.MacroState())
}

func TestMacroState_ConcurrentAccessRaceFree(t *testing.T) {
	api, _ := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// Set up initial state.
	api.macroMu.Lock()
	api.macroState = &internal.MacroExecutionState{
		Running:   true,
		MacroName: "race-test",
		Steps: []internal.MacroStepState{
			{Action: "wait", Summary: "Wait", Status: "running"},
		},
	}
	api.macroMu.Unlock()

	var wg sync.WaitGroup

	// Writer goroutines simulate onProgress callbacks.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				api.macroMu.Lock()
				api.macroState = &internal.MacroExecutionState{
					Running:     true,
					MacroName:   "race-test",
					CurrentStep: j,
					Steps: []internal.MacroStepState{
						{Action: "wait", Summary: "Wait", Status: "running", WaitMs: j * 10},
					},
				}
				api.macroMu.Unlock()
			}
		}(i)
	}

	// Reader goroutines simulate enrichState -> json.Marshal.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ms := api.MacroState()
				if ms != nil {
					// Simulate what enrichState does: read fields without lock.
					_ = ms.Running
					_ = ms.MacroName
					_ = ms.CurrentStep
					for _, s := range ms.Steps {
						_ = s.Action
						_ = s.WaitMs
					}
				}
			}
		}()
	}

	wg.Wait()
}
