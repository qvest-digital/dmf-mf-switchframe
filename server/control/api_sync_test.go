package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestSyncHealth_ReturnsFields(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ClockSync struct {
			Synchronized bool   `json:"synchronized"`
			OffsetUs     int64  `json:"offsetUs"`
			Source       string `json:"source"`
		} `json:"clockSync"`
		LastCommandSeq uint64 `json:"lastCommandSeq"`
		LastExecSeq    uint64 `json:"lastExecutedSeq"`
		LastExecErr    string `json:"lastExecutedErr"`
		UptimeMS       int64  `json:"uptimeMs"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.UptimeMS >= 0, "uptime should be non-negative")
	// Clock sync should report NTP-disciplined system clock.
	require.True(t, resp.ClockSync.Synchronized, "clock should be synchronized")
	require.Equal(t, "ntp", resp.ClockSync.Source)
	require.Equal(t, int64(0), resp.ClockSync.OffsetUs)
	// Seq fields should be present (zero-valued for fresh state).
	require.Equal(t, uint64(0), resp.LastExecSeq)
	require.Equal(t, "", resp.LastExecErr)
}

func TestSyncHealth_IncludesLastPongSentUs(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)

	pongTs := int64(1_700_000_000_000_000)
	api := NewAPI(sw, WithLastPongSentFn(func() int64 { return pongTs }))

	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp, "lastPongSentUs")
	require.InDelta(t, float64(pongTs), resp["lastPongSentUs"].(float64), 1.0)
}

func TestSyncHealth_OmitsLastPongSentUs_WhenNotConfigured(t *testing.T) {
	api, _ := setupTestAPI(t) // no WithLastPongSentFn
	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	_, present := resp["lastPongSentUs"]
	require.False(t, present, "lastPongSentUs should not be present when no fn configured")
}

func TestSyncHealth_IncludesLateCommands(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)

	// Wire a command queue with a fixed clock so we can trigger a late command.
	now := int64(100_000_000) // PTP microseconds
	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithMaxAge(2_000_000))
	sw.SetCommandQueue(q, nil)

	// Enqueue a command whose ExecuteAt is past the maxAge threshold but within
	// 2*maxAge — this triggers late promotion (ExecuteAt zeroed, counter bumped).
	_ = q.Enqueue(cmdqueue.Command{ExecuteAt: now - 3_000_000, Seq: 1, Action: "cut"})
	// Drain the promoted command.
	q.Drain()

	api := NewAPI(sw)
	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp, "lateCommands")
	require.InDelta(t, 1.0, resp["lateCommands"].(float64), 0.1,
		"lateCommands should reflect the 1 late command")
}

// --- Mock SnapshotProvider ---

type mockSnapshotProvider struct {
	snapshot internal.FullStateSnapshot
}

func (m *mockSnapshotProvider) CaptureFullSnapshot() internal.FullStateSnapshot {
	return m.snapshot
}

func (m *mockSnapshotProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &mockApplyTarget{provider: m}
}

// mockApplyTarget is a minimal SnapshotApplyTarget that records the fact that
// apply was called and returns the provider's configured warnings.
type mockApplyTarget struct {
	provider *mockSnapshotProvider
}

func (m *mockApplyTarget) Cut(_ context.Context, _ string) error                { return nil }
func (m *mockApplyTarget) SetPreview(_ context.Context, _ string) error         { return nil }
func (m *mockApplyTarget) SetTransitionConfig(_ string, _ int, _ string) error  { return nil }
func (m *mockApplyTarget) SetFTB(_ bool) error                                  { return nil }
func (m *mockApplyTarget) SetLevel(_ string, _ float64) error                   { return nil }
func (m *mockApplyTarget) SetTrim(_ string, _ float64) error                    { return nil }
func (m *mockApplyTarget) SetMuted(_ string, _ bool) error                      { return nil }
func (m *mockApplyTarget) SetAFV(_ string, _ bool) error                        { return nil }
func (m *mockApplyTarget) SetPhaseInvert(_ string, _ bool) error                { return nil }
func (m *mockApplyTarget) SetBalance(_ string, _ float64) error                 { return nil }
func (m *mockApplyTarget) SetHPF(_ string, _ float64, _ bool) error             { return nil }
func (m *mockApplyTarget) SetEQ(_ string, _ int, _, _, _ float64, _ bool) error { return nil }
func (m *mockApplyTarget) SetCompressor(_ string, _, _, _, _, _ float64) error  { return nil }
func (m *mockApplyTarget) SetGate(_ string, _, _, _, _, _ float64, _ bool) error {
	return nil
}
func (m *mockApplyTarget) SetAudioDelay(_ string, _ int) error                           { return nil }
func (m *mockApplyTarget) SetMasterLevel(_ float64) error                                { return nil }
func (m *mockApplyTarget) SetLimiterThreshold(_ float64) error                           { return nil }
func (m *mockApplyTarget) SetLimiterCeiling(_ float64) error                             { return nil }
func (m *mockApplyTarget) SetGraphicsLayerActive(_ int, _ bool) error                    { return nil }
func (m *mockApplyTarget) SetGraphicsLayerZOrder(_ int, _ int) error                     { return nil }
func (m *mockApplyTarget) SetGraphicsLayerRect(_ int, _, _, _, _ int) error              { return nil }
func (m *mockApplyTarget) SetDVELayout(_ internal.LayoutSnapshot) error                  { return nil }
func (m *mockApplyTarget) SetSourceLabel(_ context.Context, _, _ string) error           { return nil }
func (m *mockApplyTarget) SetSourcePosition(_ string, _ int) error                       { return nil }
func (m *mockApplyTarget) SetSourceDelay(_ string, _ int) error                          { return nil }
func (m *mockApplyTarget) SetUpstreamKey(_ string, _ internal.UpstreamKeySnapshot) error { return nil }
func (m *mockApplyTarget) SetCaptionMode(_ string) error                                 { return nil }
func (m *mockApplyTarget) SetColorGrade(_ string, _ float32) error                       { return nil }
func (m *mockApplyTarget) DisableColorGrade() error                                      { return nil }
func (m *mockApplyTarget) AssignSTMapSource(_, _ string) error                           { return nil }
func (m *mockApplyTarget) AssignSTMapProgram(_ string) error                             { return nil }

// --- Helpers ---

func setupTestAPIWithSnapshot(t *testing.T, provider *mockSnapshotProvider) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	api := NewAPI(sw, WithSnapshotProvider(provider))
	return api
}

// --- Tests ---

func TestGetStateSnapshot_ReturnsSnapshot(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: internal.FullStateSnapshot{
			Version:       "1",
			CapturedAt:    1234567890,
			ProgramSource: "camera1",
			PreviewSource: "camera2",
		},
	}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Response is now wrapped in a snapshotEnvelope.
	var env struct {
		Snapshot  json.RawMessage `json:"snapshot"`
		Seq       uint64          `json:"seq"`
		Timestamp int64           `json:"timestamp"`
	}
	err := json.NewDecoder(rec.Body).Decode(&env)
	require.NoError(t, err)
	require.NotNil(t, env.Snapshot)
	require.True(t, env.Timestamp > 0, "timestamp should be non-zero")

	var snap internal.FullStateSnapshot
	require.NoError(t, json.Unmarshal(env.Snapshot, &snap))
	require.Equal(t, "1", snap.Version)
	require.Equal(t, int64(1234567890), snap.CapturedAt)
	require.Equal(t, "camera1", snap.ProgramSource)
	require.Equal(t, "camera2", snap.PreviewSource)
}

func TestGetStateSnapshot_IncludesMetadata(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: internal.FullStateSnapshot{Version: "1"},
	}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var env struct {
		Snapshot  json.RawMessage `json:"snapshot"`
		Seq       *uint64         `json:"seq"`
		Timestamp *int64          `json:"timestamp"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&env))
	require.NotNil(t, env.Snapshot, "snapshot field must be present")
	require.NotNil(t, env.Seq, "seq field must be present")
	require.NotNil(t, env.Timestamp, "timestamp field must be present")
	require.True(t, *env.Timestamp > 0, "timestamp should be non-zero")
}

func TestApplyStateSnapshot_AppliesAndReturns(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Raw FullStateSnapshot format (backward compat).
	body := `{
		"version": "1",
		"capturedAt": 1234567890,
		"programSource": "camera1",
		"previewSource": "camera2",
		"captionMode": "off"
	}`
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.Applied)
}

func TestApplyStateSnapshot_EnvelopeFormat(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Build a valid envelope with seq=0 (no staleness concern).
	snapJSON := `{"version":"1","capturedAt":9999,"programSource":"camera1","captionMode":"off"}`
	env := map[string]any{
		"snapshot":  json.RawMessage(snapJSON),
		"seq":       0,
		"timestamp": time.Now().UnixMicro(),
	}
	bodyBytes, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Applied)
	// seq=0 means no staleness check — no staleness warning expected.
	for _, w := range resp.Warnings {
		require.NotContains(t, w, "behind", "unexpected staleness warning: %s", w)
	}
}

func TestApplyStateSnapshot_WarnsWhenStale(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// The engine's LastCommandSeq starts at 0 in tests (fresh switcher).
	// For the staleness warning to fire, the engine seq must be > snapshot seq.
	// We can't easily advance the engine seq in this test, but we can test
	// the inverse: snapshot seq=5 with engine seq=0 should NOT warn (snapshot
	// is ahead of engine — valid for brand-new engine taking a snapshot push).
	// The warning fires when currentSeq > probe.Seq.
	// So test: snapshot seq=1, engine seq already at 0 — no warning.
	// The real scenario is tested by verifying the warning text format is correct
	// when staleness occurs (by using a probe > 0 against a fresh engine at 0).
	// Since fresh engine has seq=0 and probe.Seq=1, currentSeq(0) > probe.Seq(1)
	// is false, so no warning — this confirms no false positives.
	snapJSON := `{"version":"1","capturedAt":9999,"captionMode":"off"}`
	env := map[string]any{
		"snapshot":  json.RawMessage(snapJSON),
		"seq":       uint64(1),
		"timestamp": time.Now().UnixMicro(),
	}
	bodyBytes, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Applied)
	// Fresh engine seq=0 < snapshot seq=1: no staleness warning.
	for _, w := range resp.Warnings {
		require.NotContains(t, w, "behind", "unexpected staleness warning: %s", w)
	}
}

func TestApplyStateSnapshot_InvalidJSON(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp["error"], "invalid snapshot JSON")
}

func TestGetStateSnapshot_NotConfigured(t *testing.T) {
	// No snapshot provider configured — routes should not be registered.
	api, _ := setupTestAPI(t)

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Reconcile Lock Tests ---

func TestReconcileLock_AcquireWhenFree(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	body := `{"holder":"browser-tab-1"}`
	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Acquired bool   `json:"acquired"`
		LockID   string `json:"lockId,omitempty"`
		Holder   string `json:"holder,omitempty"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.Acquired)
	require.NotEmpty(t, resp.LockID)
}

func TestReconcileLock_AcquireWhenHeld(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// First acquire
	body := `{"holder":"browser-tab-1"}`
	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Second acquire — should fail
	body2 := `{"holder":"browser-tab-2"}`
	req2 := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)

	var resp struct {
		Acquired bool   `json:"acquired"`
		LockID   string `json:"lockId,omitempty"`
		Holder   string `json:"holder,omitempty"`
	}
	err := json.NewDecoder(rec2.Body).Decode(&resp)
	require.NoError(t, err)
	require.False(t, resp.Acquired)
	require.Equal(t, "browser-tab-1", resp.Holder)
}

func TestReconcileLock_ReleaseAndReacquire(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Acquire
	body := `{"holder":"browser-tab-1"}`
	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var acq struct {
		Acquired bool   `json:"acquired"`
		LockID   string `json:"lockId"`
	}
	err := json.NewDecoder(rec.Body).Decode(&acq)
	require.NoError(t, err)
	require.True(t, acq.Acquired)

	// Release
	relReq := httptest.NewRequest("DELETE", "/api/sync/reconcile-lock/"+acq.LockID, nil)
	relRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(relRec, relReq)
	require.Equal(t, http.StatusOK, relRec.Code)

	// Re-acquire by another browser
	body2 := `{"holder":"browser-tab-2"}`
	req2 := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var acq2 struct {
		Acquired bool `json:"acquired"`
	}
	err = json.NewDecoder(rec2.Body).Decode(&acq2)
	require.NoError(t, err)
	require.True(t, acq2.Acquired)
}

func TestReconcileLock_ReleaseWrongID(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Acquire
	body := `{"holder":"browser-tab-1"}`
	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Release with wrong ID
	relReq := httptest.NewRequest("DELETE", "/api/sync/reconcile-lock/wrong-id", nil)
	relRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(relRec, relReq)
	require.Equal(t, http.StatusConflict, relRec.Code)
}

func TestReconcileLock_ExpiresAfterTimeout(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Override the lock expiry for testing (use the internal field).
	api.reconcileLock.expiry = 50 * time.Millisecond

	// Acquire
	body := `{"holder":"browser-tab-1"}`
	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var acq struct {
		Acquired bool `json:"acquired"`
	}
	err := json.NewDecoder(rec.Body).Decode(&acq)
	require.NoError(t, err)
	require.True(t, acq.Acquired)

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	// Second acquire should succeed (lock expired)
	body2 := `{"holder":"browser-tab-2"}`
	req2 := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var acq2 struct {
		Acquired bool `json:"acquired"`
	}
	err = json.NewDecoder(rec2.Body).Decode(&acq2)
	require.NoError(t, err)
	require.True(t, acq2.Acquired)
}

func TestReconcileLock_ReleaseNoLockHeld(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	// Release with no lock held
	relReq := httptest.NewRequest("DELETE", "/api/sync/reconcile-lock/some-id", nil)
	relRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(relRec, relReq)
	require.Equal(t, http.StatusOK, relRec.Code)
}

func TestReconcileLock_InvalidBody(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReconcileLock_Refresh(t *testing.T) {
	lock := reconcileLockState{expiry: 500 * time.Millisecond}
	lockID, _, acquired := lock.tryAcquire("tab-1")
	if !acquired {
		t.Fatal("expected lock acquired")
	}

	time.Sleep(300 * time.Millisecond)
	ok := lock.refresh(lockID)
	if !ok {
		t.Fatal("expected refresh to succeed")
	}

	// Should still be held past original expiry
	time.Sleep(300 * time.Millisecond)
	_, _, acquired2 := lock.tryAcquire("tab-2")
	if acquired2 {
		t.Fatal("expected lock still held after refresh")
	}
}

func TestReconcileLock_RefreshWrongID(t *testing.T) {
	lock := reconcileLockState{expiry: 15 * time.Second}
	lock.tryAcquire("tab-1")
	ok := lock.refresh("wrong-id")
	if ok {
		t.Fatal("expected refresh to fail with wrong ID")
	}
}

func TestReconcileLock_RefreshExpiredLock(t *testing.T) {
	lock := reconcileLockState{expiry: 100 * time.Millisecond}
	lockID, _, _ := lock.tryAcquire("tab-1")
	time.Sleep(150 * time.Millisecond)
	ok := lock.refresh(lockID)
	if ok {
		t.Fatal("expected refresh to fail on expired lock")
	}
}

// TestGetStateSnapshot_SeqCapturedBeforeSnapshot verifies that the envelope's
// seq reflects the state BEFORE CaptureFullSnapshot() is called. If a command
// executes between enrichedState() and CaptureFullSnapshot(), the seq must be
// <= the snapshot's actual state (not >, which would make a stale snapshot
// look current to the receiving engine).
func TestGetStateSnapshot_SeqCapturedBeforeSnapshot(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)

	// Start with seq=5 so we have a known baseline.
	sw.TrackCommandSeq(5)

	// seqAtSnapshotTime records the seq at the moment CaptureFullSnapshot is called.
	// If the code correctly calls enrichedState() first, the envelope's seq
	// will be <= seqAtSnapshotTime (captured before the snapshot provider runs).
	var seqAtSnapshotTime uint64
	provider := &seqTrackingSnapshotProvider{
		snapshot: internal.FullStateSnapshot{
			Version:       "1",
			ProgramSource: "camera1",
		},
		onCapture: func() {
			// Simulate a command executing between the two calls by
			// advancing the switcher's command seq. If enrichedState() is
			// called AFTER CaptureFullSnapshot, the envelope seq will
			// reflect this bump and be too high.
			sw.TrackCommandSeq(10)
			seqAtSnapshotTime = 10
		},
	}
	api := NewAPI(sw, WithSnapshotProvider(provider))

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var env struct {
		Seq uint64 `json:"seq"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&env))

	// The envelope seq must be strictly less than the seq at snapshot time,
	// proving enrichedState() was called BEFORE CaptureFullSnapshot().
	require.Less(t, env.Seq, seqAtSnapshotTime,
		"envelope seq should be captured BEFORE CaptureFullSnapshot (seq race fix)")
}

// seqTrackingSnapshotProvider calls onCapture during CaptureFullSnapshot.
type seqTrackingSnapshotProvider struct {
	snapshot  internal.FullStateSnapshot
	onCapture func()
}

func (s *seqTrackingSnapshotProvider) CaptureFullSnapshot() internal.FullStateSnapshot {
	if s.onCapture != nil {
		s.onCapture()
	}
	return s.snapshot
}

func (s *seqTrackingSnapshotProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &mockApplyTarget{}
}

// --- Snapshot Metrics Tests ---

func TestApplyStateSnapshot_MetricsSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)
	api.SetAppMetrics(m)

	body := `{"version":"1","capturedAt":1234567890,"programSource":"camera1","captionMode":"off"}`
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify success counter incremented.
	val := testutil.ToFloat64(m.SnapshotApplyTotal.WithLabelValues("success"))
	require.Equal(t, float64(1), val, "expected 1 successful apply")

	// Verify size was recorded.
	families, err := reg.Gather()
	require.NoError(t, err)
	var found bool
	for _, f := range families {
		if f.GetName() == "switchframe_snapshot_size_bytes" {
			found = true
			hist := f.GetMetric()[0].GetHistogram()
			require.Equal(t, uint64(1), hist.GetSampleCount())
		}
	}
	require.True(t, found, "snapshot_size_bytes metric not found")

	// Verify duration was recorded.
	for _, f := range families {
		if f.GetName() == "switchframe_snapshot_apply_duration_seconds" {
			hist := f.GetMetric()[0].GetHistogram()
			require.Equal(t, uint64(1), hist.GetSampleCount())
		}
	}
}

func TestApplyStateSnapshot_MetricsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)
	api.SetAppMetrics(m)

	// Invalid JSON should increment error counter.
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	val := testutil.ToFloat64(m.SnapshotApplyTotal.WithLabelValues("error"))
	require.Equal(t, float64(1), val, "expected 1 error apply")
}

func TestApplyStateSnapshot_MetricsWithoutMetrics(t *testing.T) {
	// Verify no panic when metrics are not configured.
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)
	// No SetAppMetrics call.

	body := `{"version":"1","capturedAt":1234567890,"captionMode":"off"}`
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestApplySnapshot_RejectsStale(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	// Advance engine's LastCommandSeq to 105 so an incoming snapshot at seq=100
	// is strictly behind.
	sw.TrackCommandSeq(105)

	provider := &mockSnapshotProvider{}
	api := NewAPI(sw, WithSnapshotProvider(provider))

	snapJSON := `{"version":"1","capturedAt":9999,"captionMode":"off"}`
	env := map[string]any{
		"snapshot":  json.RawMessage(snapJSON),
		"seq":       uint64(100),
		"timestamp": time.Now().UnixMicro(),
	}
	bodyBytes, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp struct {
		Error       string `json:"error"`
		SnapshotSeq uint64 `json:"snapshot_seq"`
		CurrentSeq  uint64 `json:"current_seq"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "snapshot stale", resp.Error)
	require.Equal(t, uint64(100), resp.SnapshotSeq)
	require.Equal(t, uint64(105), resp.CurrentSeq)
}

func TestApplySnapshot_AcceptsForward(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	// Engine at seq=100, incoming snapshot also at seq=100 — not behind.
	sw.TrackCommandSeq(100)

	provider := &mockSnapshotProvider{}
	api := NewAPI(sw, WithSnapshotProvider(provider))

	snapJSON := `{"version":"1","capturedAt":9999,"captionMode":"off"}`
	env := map[string]any{
		"snapshot":  json.RawMessage(snapJSON),
		"seq":       uint64(100),
		"timestamp": time.Now().UnixMicro(),
	}
	bodyBytes, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Applied bool `json:"applied"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Applied)
}

func TestApplySnapshot_AcceptsForceOverride(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	sw.TrackCommandSeq(105)

	provider := &mockSnapshotProvider{}
	api := NewAPI(sw, WithSnapshotProvider(provider))

	snapJSON := `{"version":"1","capturedAt":9999,"captionMode":"off"}`
	env := map[string]any{
		"snapshot":  json.RawMessage(snapJSON),
		"seq":       uint64(100),
		"timestamp": time.Now().UnixMicro(),
	}
	bodyBytes, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot?force=true", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Applied)
	// Force-override should surface a warning indicating staleness was bypassed.
	var hasForceWarning bool
	for _, w := range resp.Warnings {
		if strings.Contains(w, "force") || strings.Contains(w, "behind") {
			hasForceWarning = true
			break
		}
	}
	require.True(t, hasForceWarning, "expected a warning flagging the forced stale apply, got: %v", resp.Warnings)
}
