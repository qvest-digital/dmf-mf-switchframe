package control

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

func TestHandleRunMacro_ConcurrentRun(t *testing.T) {
	api, store := setupMacroTestAPI(t)

	api.SetBroadcastFunc(func() {})

	// Save a macro with a long wait step so the first run blocks.
	err := store.Save(macro.Macro{
		Name: "long-wait",
		Steps: []macro.Step{
			{Action: macro.ActionWait, Params: map[string]interface{}{"ms": float64(5000)}},
		},
	})
	require.NoError(t, err)

	// Channel to signal when the first run goroutine has started the macro.
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		body := `{}`
		req := httptest.NewRequest("POST", "/api/macros/long-wait/run", strings.NewReader(body))
		rec := httptest.NewRecorder()

		// Signal that we've dispatched the request — the macro will block on
		// the wait step, keeping macroState.Running = true.
		close(started)
		api.mux.ServeHTTP(rec, req)
	}()

	<-started
	// Give the goroutine a moment to enter the handler and acquire the lock.
	time.Sleep(100 * time.Millisecond)

	// Second run while first is still running should get 409 Conflict.
	body := `{}`
	req := httptest.NewRequest("POST", "/api/macros/long-wait/run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

	// Clean up: cancel the running macro so the goroutine exits.
	api.macroMu.Lock()
	cancel := api.macroCancel
	api.macroMu.Unlock()
	require.NotNil(t, cancel, "macroCancel should be set while macro is running")
	cancel()

	// Wait for the first run goroutine to finish.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first macro run did not finish after cancel")
	}
}
